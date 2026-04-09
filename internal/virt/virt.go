package virt

import "io"

// VMConfig holds configuration for creating a virtual machine.
type VMConfig struct {
	Name       string
	KernelPath string
	InitrdPath string
	CmdLine    string
	CPUs       uint
	MemoryMB   uint64
	ShareDir   string // Host directory to share via virtio-fs (empty = none)
	ShareTag   string // virtio-fs mount tag (default: "workspace")
}

// VM represents a running virtual machine.
type VM interface {
	// Start boots the VM and blocks until it's running.
	Start() error
	// Stop requests a graceful shutdown.
	Stop() error
	// Wait blocks until the VM stops.
	Wait() error
	// Console returns the read/write streams for the VM console.
	Console() (io.Reader, io.Writer)
}

// Hypervisor creates and manages virtual machines.
type Hypervisor interface {
	// Available reports whether this hypervisor is usable on the current system.
	Available() bool
	// CreateVM creates a new virtual machine from the given config.
	CreateVM(cfg VMConfig) (VM, error)
}
