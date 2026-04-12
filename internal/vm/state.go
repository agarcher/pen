// Package vm provides VM lifecycle management including state tracking,
// console attachment, and lock-based liveness detection.
package vm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const (
	configDir = ".config/pen"
	vmsDir    = "vms"
	vmFile    = "vm.json"
	lockFile  = "lock"
)

// VMState represents a VM's persisted configuration.
type VMState struct {
	Name      string    `json:"name"`
	Dir       string    `json:"dir"`
	CPUs      uint      `json:"cpus"`
	MemoryMB  uint64    `json:"memory_mb"`
	Profile   string    `json:"profile,omitempty"`
	SetupHash string    `json:"setup_hash,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Status describes the current state of a VM.
type Status string

const (
	StatusRunning Status = "running"
	StatusStopped Status = "stopped"
)

// VMInfo combines persisted state with runtime status.
type VMInfo struct {
	VMState
	Status Status `json:"status"`
	PID    int    `json:"pid,omitempty"`
}

// StateDir returns the base directory for all VM state.
func StateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, configDir, vmsDir), nil
}

// vmDir returns the directory for a specific VM's state.
func vmDir(name string) (string, error) {
	base, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, name), nil
}

// Save persists the VM state to disk.
func Save(state *VMState) error {
	dir, err := vmDir(state.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating VM state dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling VM state: %w", err)
	}

	return os.WriteFile(filepath.Join(dir, vmFile), data, 0644)
}

// Load reads a VM's persisted state.
func Load(name string) (*VMState, error) {
	dir, err := vmDir(name)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(filepath.Join(dir, vmFile))
	if err != nil {
		return nil, fmt.Errorf("reading VM state for %q: %w", name, err)
	}

	var state VMState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing VM state for %q: %w", name, err)
	}
	return &state, nil
}

// Exists reports whether a VM with the given name has persisted state.
func Exists(name string) bool {
	dir, err := vmDir(name)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, vmFile))
	return err == nil
}

// Lock represents an exclusive lock on a VM's state directory. It is held
// for the lifetime of a `pen shell` process and released on Release.
//
// Liveness checks (IsRunning) work by attempting to acquire the same lock
// non-blocking; success means no other process holds it (i.e., the VM is
// stopped). This avoids the PID-reuse problem of comparing raw PID numbers,
// since the OS releases flock(2) locks automatically on process exit even
// after a crash.
type Lock struct {
	f *os.File
}

// AcquireLock takes an exclusive non-blocking flock on the VM's lock file
// and writes the current PID into it. Returns an error containing the
// existing holder's PID if the lock is already held.
func AcquireLock(name string) (*Lock, error) {
	dir, err := vmDir(name)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating VM state dir: %w", err)
	}

	path := filepath.Join(dir, lockFile)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			pid := ReadPID(name)
			return nil, fmt.Errorf("VM %q is already running (PID %d)", name, pid)
		}
		return nil, fmt.Errorf("locking VM state: %w", err)
	}

	// Truncate any stale PID and write our own.
	if err := f.Truncate(0); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, fmt.Errorf("truncating lock file: %w", err)
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, fmt.Errorf("writing PID: %w", err)
	}

	return &Lock{f: f}, nil
}

// Release closes the lock file, releasing the OS lock and making the
// VM's state directory available for a future AcquireLock.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
	l.f = nil
}

// ReadPID returns the PID recorded in the lock file, or 0 if none.
// Note: this PID may be stale if the holder crashed; always combine with
// IsRunning to determine if the process is actually alive.
func ReadPID(name string) int {
	dir, err := vmDir(name)
	if err != nil {
		return 0
	}
	data, err := os.ReadFile(filepath.Join(dir, lockFile))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return 0
	}
	return pid
}

// IsRunning checks if any process holds the VM's lock. This is reliable
// across crashes because the OS releases flock(2) locks on process exit,
// regardless of how the process terminated.
func IsRunning(name string) bool {
	dir, err := vmDir(name)
	if err != nil {
		return false
	}
	path := filepath.Join(dir, lockFile)

	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return false // file doesn't exist → no holder
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// EWOULDBLOCK means another process holds the lock → running.
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	// We took the lock, so it was free → not running. Release.
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// List returns info for all known VMs.
func List() ([]VMInfo, error) {
	base, err := StateDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading VM state dir: %w", err)
	}

	var vms []VMInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		state, err := Load(name)
		if err != nil {
			continue // skip corrupt entries
		}

		info := VMInfo{VMState: *state}
		if IsRunning(name) {
			info.Status = StatusRunning
			info.PID = ReadPID(name)
		} else {
			info.Status = StatusStopped
		}
		vms = append(vms, info)
	}
	return vms, nil
}

// Delete removes all state for a VM. Returns an error if it's running.
func Delete(name string) error {
	if IsRunning(name) {
		return fmt.Errorf("VM %q is running (PID %d); stop it first", name, ReadPID(name))
	}

	dir, err := vmDir(name)
	if err != nil {
		return err
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("VM %q not found", name)
	}

	return os.RemoveAll(dir)
}

// StopByPID sends SIGTERM to the process owning the VM. The signal handler
// in `pen shell` catches this and triggers a graceful VM shutdown via the
// hypervisor's RequestStop (ACPI power button), so the guest kernel has a
// chance to sync filesystems before halting.
func StopByPID(name string) error {
	if !IsRunning(name) {
		return fmt.Errorf("VM %q is not running", name)
	}

	pid := ReadPID(name)
	if pid == 0 {
		return fmt.Errorf("VM %q has no recorded PID", name)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("finding process %d: %w", pid, err)
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("sending SIGTERM to %d: %w", pid, err)
	}

	return nil
}
