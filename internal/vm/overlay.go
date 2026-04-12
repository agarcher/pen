package vm

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const overlayFile = "overlay.img"

// DefaultOverlaySize is the default size for a new overlay disk.
const DefaultOverlaySize int64 = 10 * 1024 * 1024 * 1024 // 10 GiB

// OverlayPath returns the path to a VM's overlay disk image.
func OverlayPath(name string) (string, error) {
	dir, err := vmDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, overlayFile), nil
}

// EnsureOverlay creates a sparse overlay disk image of the given size if one
// does not already exist for the VM. If the file exists, the requested size
// is ignored — overlay disks are sized once at creation time. Returns the
// path to the overlay image.
func EnsureOverlay(name string, sizeBytes int64) (string, error) {
	path, err := OverlayPath(name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("creating VM state dir: %w", err)
	}

	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("checking overlay disk: %w", err)
	}

	// Create a sparse file. The guest formats it on first boot.
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("creating overlay disk: %w", err)
	}
	if err := f.Truncate(sizeBytes); err != nil {
		f.Close()
		os.Remove(path)
		return "", fmt.Errorf("sizing overlay disk: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("closing overlay disk: %w", err)
	}
	return path, nil
}

// OverlayDiskUsage returns the actual bytes on disk for a VM's overlay image.
// For sparse files, this reflects only the allocated blocks, not the logical
// size. Returns 0 if the overlay file does not exist.
func OverlayDiskUsage(name string) (int64, error) {
	path, err := OverlayPath(name)
	if err != nil {
		return 0, err
	}

	var stat syscall.Stat_t
	if err := syscall.Stat(path, &stat); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("stat overlay disk: %w", err)
	}
	return stat.Blocks * 512, nil
}

// ParseDiskSize parses a human-readable size string like "10G", "512M",
// "2048" (bytes if no suffix). Suffixes are powers of 1024. Decimal values
// are not supported.
func ParseDiskSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}

	// Strip an optional trailing "B" or "iB" so "10G", "10GB", and "10GiB"
	// all parse identically.
	upper := strings.ToUpper(s)
	upper = strings.TrimSuffix(upper, "IB")
	upper = strings.TrimSuffix(upper, "B")

	if upper == "" {
		return 0, fmt.Errorf("invalid size %q", s)
	}

	var mult int64 = 1
	last := upper[len(upper)-1]
	switch last {
	case 'K':
		mult = 1024
	case 'M':
		mult = 1024 * 1024
	case 'G':
		mult = 1024 * 1024 * 1024
	case 'T':
		mult = 1024 * 1024 * 1024 * 1024
	}
	if mult > 1 {
		upper = upper[:len(upper)-1]
	}

	n, err := strconv.ParseInt(upper, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", s, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("size must be positive, got %q", s)
	}
	return n * mult, nil
}
