package virt

import (
	"fmt"
	"io"
	"os"
	"sync"

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

	// Console: create pipes for bidirectional host ↔ guest I/O.
	// guestIn/guestOut are given to the VM attachment.
	// hostIn/hostOut are used by the host to read/write.
	guestIn, hostOut, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("creating guest input pipe: %w", err)
	}
	hostIn, guestOut, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("creating guest output pipe: %w", err)
	}

	attachment, err := vz.NewFileHandleSerialPortAttachment(guestIn, guestOut)
	if err != nil {
		return nil, fmt.Errorf("creating serial attachment: %w", err)
	}

	consolePort, err := vz.NewVirtioConsolePortConfiguration(
		vz.WithVirtioConsolePortConfigurationAttachment(attachment),
		vz.WithVirtioConsolePortConfigurationIsConsole(true),
	)
	if err != nil {
		return nil, fmt.Errorf("creating console port config: %w", err)
	}

	consoleDevice, err := vz.NewVirtioConsoleDeviceConfiguration()
	if err != nil {
		return nil, fmt.Errorf("creating console device config: %w", err)
	}
	consoleDevice.SetVirtioConsolePortConfiguration(0, consolePort)
	vzConfig.SetConsoleDevicesVirtualMachineConfiguration([]vz.ConsoleDeviceConfiguration{consoleDevice})

	// Entropy device (recommended for Linux guests).
	entropyDevice, err := vz.NewVirtioEntropyDeviceConfiguration()
	if err != nil {
		return nil, fmt.Errorf("creating entropy device: %w", err)
	}
	vzConfig.SetEntropyDevicesVirtualMachineConfiguration([]*vz.VirtioEntropyDeviceConfiguration{entropyDevice})

	// Network: NAT.
	natAttachment, err := vz.NewNATNetworkDeviceAttachment()
	if err != nil {
		return nil, fmt.Errorf("creating NAT attachment: %w", err)
	}
	netDevice, err := vz.NewVirtioNetworkDeviceConfiguration(natAttachment)
	if err != nil {
		return nil, fmt.Errorf("creating network device: %w", err)
	}
	vzConfig.SetNetworkDevicesVirtualMachineConfiguration([]*vz.VirtioNetworkDeviceConfiguration{netDevice})

	// Directory sharing (virtio-fs).
	if cfg.ShareDir != "" {
		tag := cfg.ShareTag
		if tag == "" {
			tag = "workspace"
		}
		sharedDir, err := vz.NewSharedDirectory(cfg.ShareDir, false)
		if err != nil {
			return nil, fmt.Errorf("creating shared directory: %w", err)
		}
		share, err := vz.NewSingleDirectoryShare(sharedDir)
		if err != nil {
			return nil, fmt.Errorf("creating directory share: %w", err)
		}
		fsDevice, err := vz.NewVirtioFileSystemDeviceConfiguration(tag)
		if err != nil {
			return nil, fmt.Errorf("creating virtio-fs device: %w", err)
		}
		fsDevice.SetDirectoryShare(share)
		vzConfig.SetDirectorySharingDevicesVirtualMachineConfiguration([]vz.DirectorySharingDeviceConfiguration{fsDevice})
	}

	validated, err := vzConfig.Validate()
	if err != nil {
		return nil, fmt.Errorf("validating VM config: %w", err)
	}
	if !validated {
		return nil, fmt.Errorf("VM configuration is not valid")
	}

	machine, err := vz.NewVirtualMachine(vzConfig)
	if err != nil {
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
	hostIn  *os.File // read from guest
	hostOut *os.File // write to guest
	done    chan struct{}
	once    sync.Once
}

func (vm *appleVM) Start() error {
	vm.done = make(chan struct{})

	if err := vm.machine.Start(); err != nil {
		return fmt.Errorf("starting VM: %w", err)
	}

	// Monitor state changes and close done when the VM stops.
	go func() {
		ch := vm.machine.StateChangedNotify()
		for state := range ch {
			if state == vz.VirtualMachineStateStopped || state == vz.VirtualMachineStateError {
				vm.once.Do(func() { close(vm.done) })
				return
			}
		}
	}()

	return nil
}

func (vm *appleVM) Stop() error {
	if vm.machine.CanRequestStop() {
		_, err := vm.machine.RequestStop()
		return err
	}
	if vm.machine.CanStop() {
		return vm.machine.Stop()
	}
	return nil
}

func (vm *appleVM) Wait() error {
	<-vm.done
	return nil
}

func (vm *appleVM) Console() (io.Reader, io.Writer) {
	return vm.hostIn, vm.hostOut
}
