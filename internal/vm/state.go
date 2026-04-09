// Package vm provides VM lifecycle management including state tracking,
// console attachment, and process-level liveness detection.
package vm

import (
	"encoding/json"
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
	pidFile   = "pid"
)

// VMState represents a VM's persisted configuration.
type VMState struct {
	Name      string    `json:"name"`
	Dir       string    `json:"dir"`
	CPUs      uint      `json:"cpus"`
	MemoryMB  uint64    `json:"memory_mb"`
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

// WritePID records the current process as the owner of this VM.
func WritePID(name string) error {
	dir, err := vmDir(name)
	if err != nil {
		return err
	}
	pid := strconv.Itoa(os.Getpid())
	return os.WriteFile(filepath.Join(dir, pidFile), []byte(pid), 0644)
}

// ClearPID removes the PID file, marking the VM as stopped.
func ClearPID(name string) {
	dir, err := vmDir(name)
	if err != nil {
		return
	}
	os.Remove(filepath.Join(dir, pidFile))
}

// ReadPID returns the PID recorded for a VM, or 0 if none.
func ReadPID(name string) int {
	dir, err := vmDir(name)
	if err != nil {
		return 0
	}
	data, err := os.ReadFile(filepath.Join(dir, pidFile))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return 0
	}
	return pid
}

// IsRunning checks if the VM's owner process is still alive.
func IsRunning(name string) bool {
	pid := ReadPID(name)
	if pid == 0 {
		return false
	}
	// Signal 0 checks process existence without sending a signal.
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
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
			// Clean up stale PID file.
			ClearPID(name)
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

// StopByPID sends SIGTERM to the process owning the VM.
func StopByPID(name string) error {
	pid := ReadPID(name)
	if pid == 0 {
		return fmt.Errorf("VM %q is not running", name)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("finding process %d: %w", pid, err)
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// Process already dead — clean up.
		ClearPID(name)
		return fmt.Errorf("VM %q is not running", name)
	}

	return nil
}
