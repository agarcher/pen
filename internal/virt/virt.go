package virt

import "io"

// Share is a host directory exposed to the guest via virtio-fs.
type Share struct {
	HostPath string
	Tag      string
	ReadOnly bool
}

// Disk is a host file exposed to the guest as a virtio-blk block device.
// The first attached disk appears as /dev/vda inside the guest.
type Disk struct {
	ImagePath string
	ReadOnly  bool
}

// VMConfig holds configuration for creating a virtual machine.
type VMConfig struct {
	Name       string
	KernelPath string
	InitrdPath string
	CmdLine    string
	CPUs       uint
	MemoryMB   uint64
	Shares     []Share
	Disks      []Disk
}

// VM represents a running virtual machine.
type VM interface {
	// Start boots the VM and blocks until it's running.
	Start() error
	// Stop requests a graceful shutdown.
	Stop() error
	// Wait blocks until the VM stops.
	Wait() error
	// Console returns the read/write streams for the VM console (hvc0).
	Console() (io.Reader, io.Writer)
}

// Hypervisor creates and manages virtual machines.
type Hypervisor interface {
	// Available reports whether this hypervisor is usable on the current system.
	Available() bool
	// CreateVM creates a new virtual machine from the given config.
	CreateVM(cfg VMConfig) (VM, error)
}
