package virt

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/Code-Hex/vz/v3"
)

// AppleHypervisor implements Hypervisor using Apple Virtualization.framework.
type AppleHypervisor struct{}

func NewAppleHypervisor() *AppleHypervisor {
	return &AppleHypervisor{}
}

func (h *AppleHypervisor) Available() bool {
	// Availability is checked implicitly by vz constructors;
	// they return errors on unsupported macOS versions.
	return true
}

func (h *AppleHypervisor) CreateVM(cfg VMConfig) (VM, error) {
	bootLoader, err := vz.NewLinuxBootLoader(
		cfg.KernelPath,
		vz.WithInitrd(cfg.InitrdPath),
		vz.WithCommandLine(cfg.CmdLine),
	)
	if err != nil {
		return nil, fmt.Errorf("creating boot loader: %w", err)
	}

	memBytes := cfg.MemoryMB * 1024 * 1024
	vzConfig, err := vz.NewVirtualMachineConfiguration(bootLoader, cfg.CPUs, memBytes)
	if err != nil {
		return nil, fmt.Errorf("creating VM config: %w", err)
	}

	// Port 0: interactive console (hvc0) — bidirectional pipes.
	// Track all created pipes so we can close them on any error path.
	var pipes []*os.File
	cleanupPipes := func() {
		for _, p := range pipes {
			p.Close()
		}
	}

	guestIn, hostOut, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("creating guest input pipe: %w", err)
	}
	pipes = append(pipes, guestIn, hostOut)

	hostIn, guestOut, err := os.Pipe()
	if err != nil {
		cleanupPipes()
		return nil, fmt.Errorf("creating guest output pipe: %w", err)
	}
	pipes = append(pipes, hostIn, guestOut)

	consoleAttachment, err := vz.NewFileHandleSerialPortAttachment(guestIn, guestOut)
	if err != nil {
		cleanupPipes()
		return nil, fmt.Errorf("creating console attachment: %w", err)
	}

	consolePort, err := vz.NewVirtioConsolePortConfiguration(
		vz.WithVirtioConsolePortConfigurationAttachment(consoleAttachment),
		vz.WithVirtioConsolePortConfigurationIsConsole(true),
	)
	if err != nil {
		cleanupPipes()
		return nil, fmt.Errorf("creating console port config: %w", err)
	}

	consoleDevice, err := vz.NewVirtioConsoleDeviceConfiguration()
	if err != nil {
		cleanupPipes()
		return nil, fmt.Errorf("creating console device config: %w", err)
	}
	consoleDevice.SetVirtioConsolePortConfiguration(0, consolePort)
	vzConfig.SetConsoleDevicesVirtualMachineConfiguration([]vz.ConsoleDeviceConfiguration{consoleDevice})

	// Entropy device (recommended for Linux guests).
	entropyDevice, err := vz.NewVirtioEntropyDeviceConfiguration()
	if err != nil {
		cleanupPipes()
		return nil, fmt.Errorf("creating entropy device: %w", err)
	}
	vzConfig.SetEntropyDevicesVirtualMachineConfiguration([]*vz.VirtioEntropyDeviceConfiguration{entropyDevice})

	// Network: NAT.
	natAttachment, err := vz.NewNATNetworkDeviceAttachment()
	if err != nil {
		cleanupPipes()
		return nil, fmt.Errorf("creating NAT attachment: %w", err)
	}
	netDevice, err := vz.NewVirtioNetworkDeviceConfiguration(natAttachment)
	if err != nil {
		cleanupPipes()
		return nil, fmt.Errorf("creating network device: %w", err)
	}
	vzConfig.SetNetworkDevicesVirtualMachineConfiguration([]*vz.VirtioNetworkDeviceConfiguration{netDevice})

	// Directory sharing (virtio-fs). One device per Share entry.
	if len(cfg.Shares) > 0 {
		fsDevices := make([]vz.DirectorySharingDeviceConfiguration, 0, len(cfg.Shares))
		for _, s := range cfg.Shares {
			tag := s.Tag
			if tag == "" {
				tag = "workspace"
			}
			sharedDir, err := vz.NewSharedDirectory(s.HostPath, s.ReadOnly)
			if err != nil {
				cleanupPipes()
				return nil, fmt.Errorf("creating shared directory %q: %w", s.HostPath, err)
			}
			share, err := vz.NewSingleDirectoryShare(sharedDir)
			if err != nil {
				cleanupPipes()
				return nil, fmt.Errorf("creating directory share %q: %w", s.HostPath, err)
			}
			fsDevice, err := vz.NewVirtioFileSystemDeviceConfiguration(tag)
			if err != nil {
				cleanupPipes()
				return nil, fmt.Errorf("creating virtio-fs device %q: %w", tag, err)
			}
			fsDevice.SetDirectoryShare(share)
			fsDevices = append(fsDevices, fsDevice)
		}
		vzConfig.SetDirectorySharingDevicesVirtualMachineConfiguration(fsDevices)
	}

	// Block devices (virtio-blk). The first disk appears as /dev/vda.
	if len(cfg.Disks) > 0 {
		storageDevices := make([]vz.StorageDeviceConfiguration, 0, len(cfg.Disks))
		for _, d := range cfg.Disks {
			attachment, err := vz.NewDiskImageStorageDeviceAttachment(d.ImagePath, d.ReadOnly)
			if err != nil {
				cleanupPipes()
				return nil, fmt.Errorf("creating disk attachment %q: %w", d.ImagePath, err)
			}
			blockDevice, err := vz.NewVirtioBlockDeviceConfiguration(attachment)
			if err != nil {
				cleanupPipes()
				return nil, fmt.Errorf("creating block device %q: %w", d.ImagePath, err)
			}
			storageDevices = append(storageDevices, blockDevice)
		}
		vzConfig.SetStorageDevicesVirtualMachineConfiguration(storageDevices)
	}

	validated, err := vzConfig.Validate()
	if err != nil {
		cleanupPipes()
		return nil, fmt.Errorf("validating VM config: %w", err)
	}
	if !validated {
		cleanupPipes()
		return nil, fmt.Errorf("VM configuration is not valid")
	}

	machine, err := vz.NewVirtualMachine(vzConfig)
	if err != nil {
		cleanupPipes()
		return nil, fmt.Errorf("creating virtual machine: %w", err)
	}

	return &appleVM{
		machine: machine,
		hostIn:  hostIn,
		hostOut: hostOut,
	}, nil
}

type appleVM struct {
	machine *vz.VirtualMachine
	hostIn  *os.File // console: read from guest
	hostOut *os.File // console: write to guest
	done    chan struct{}
	once    sync.Once
}

func (vm *appleVM) Start() error {
	vm.done = make(chan struct{})

	// Start monitoring state changes BEFORE calling machine.Start so we
	// don't miss early Stopped/Error transitions (e.g., immediate boot
	// failures), which would otherwise leave Wait() blocked forever.
	go func() {
		ch := vm.machine.StateChangedNotify()
		for state := range ch {
			if state == vz.VirtualMachineStateStopped || state == vz.VirtualMachineStateError {
				vm.once.Do(func() {
					// Close the host read side of the console pipe so
					// io.Copy in AttachConsole sees EOF and returns.
					if vm.hostIn != nil {
						vm.hostIn.Close()
					}
					close(vm.done)
				})
				return
			}
		}
	}()

	if err := vm.machine.Start(); err != nil {
		return fmt.Errorf("starting VM: %w", err)
	}

	return nil
}

// Stop blocks until the VM has fully stopped. It first attempts a graceful
// ACPI shutdown via RequestStop, and falls back to a forceful Stop if the
// guest does not honor the ACPI signal within the timeout.
//
// In our minimal Alpine image there is no acpid/systemd to handle the
// ACPI power event, so the graceful path almost always falls through —
// but the structure is in place for future images that include one.
//
// This method is safe to call multiple times. Once the VM has stopped,
// subsequent calls return immediately.
func (vm *appleVM) Stop() error {
	if vm.machine.State() == vz.VirtualMachineStateStopped ||
		vm.machine.State() == vz.VirtualMachineStateError {
		return nil
	}

	const gracefulTimeout = 2 * time.Second

	if vm.machine.CanRequestStop() {
		if _, err := vm.machine.RequestStop(); err == nil {
			select {
			case <-vm.done:
				return nil
			case <-time.After(gracefulTimeout):
				// Fall through to forceful stop.
			}
		}
	}

	if vm.machine.CanStop() {
		if err := vm.machine.Stop(); err != nil {
			return fmt.Errorf("forcing stop: %w", err)
		}
	}

	// Wait for the state machine to settle.
	<-vm.done
	return nil
}

func (vm *appleVM) Wait() error {
	<-vm.done
	return nil
}

func (vm *appleVM) Console() (io.Reader, io.Writer) {
	return vm.hostIn, vm.hostOut
}
