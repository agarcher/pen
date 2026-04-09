package image

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	configDir  = ".config/pen"
	imagesDir  = "images"
	kernelFile = "vmlinuz"
	initrdFile = "initrd"
)

// Paths holds the resolved paths to kernel and initrd.
type Paths struct {
	Kernel string
	Initrd string
}

// Resolve returns the paths to the kernel and initrd images, or an error if
// they don't exist. Images are expected in ~/.config/pen/images/.
func Resolve() (*Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}

	dir := filepath.Join(home, configDir, imagesDir)
	kernel := filepath.Join(dir, kernelFile)
	initrd := filepath.Join(dir, initrdFile)

	if _, err := os.Stat(kernel); err != nil {
		return nil, fmt.Errorf("kernel not found at %s: %w\n\nRun the image build script first:\n  make -C images/alpine build", kernel, err)
	}
	if _, err := os.Stat(initrd); err != nil {
		return nil, fmt.Errorf("initrd not found at %s: %w\n\nRun the image build script first:\n  make -C images/alpine build", initrd, err)
	}

	return &Paths{Kernel: kernel, Initrd: initrd}, nil
}

// Dir returns the image directory path.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, configDir, imagesDir), nil
}
