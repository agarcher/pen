package image

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// httpClient is used for image downloads. The timeout covers the full
// request including the body transfer, so it must be generous enough for
// a multi-megabyte initrd over a slow connection.
var httpClient = &http.Client{
	Timeout: 5 * time.Minute,
}

const (
	configDir  = ".config/pen"
	imagesDir  = "images"
	kernelFile = "vmlinuz"
	initrdFile = "initrd"

	// GitHub Release base URL for pre-built images.
	// Images are published as pen-image-<arch>.tar.gz containing vmlinuz + initrd.
	releaseBaseURL = "https://github.com/agarcher/pen/releases/download"
)

// Paths holds the resolved paths to kernel and initrd.
type Paths struct {
	Kernel string
	Initrd string
}

// Resolve returns the paths to the kernel and initrd images.
// If images are not cached locally, it downloads them from the latest
// GitHub Release.
func Resolve() (*Paths, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}

	kernel := filepath.Join(dir, kernelFile)
	initrd := filepath.Join(dir, initrdFile)

	// Check if both files exist locally.
	_, kernelErr := os.Stat(kernel)
	_, initrdErr := os.Stat(initrd)

	if kernelErr == nil && initrdErr == nil {
		return &Paths{Kernel: kernel, Initrd: initrd}, nil
	}

	// Need to download.
	fmt.Fprintf(os.Stderr, "pen: images not found, downloading...\n")
	if err := download(dir); err != nil {
		return nil, fmt.Errorf("downloading images: %w", err)
	}

	// Verify they exist now.
	if _, err := os.Stat(kernel); err != nil {
		return nil, fmt.Errorf("kernel not found after download: %w", err)
	}
	if _, err := os.Stat(initrd); err != nil {
		return nil, fmt.Errorf("initrd not found after download: %w", err)
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

// goarchToAlpine maps Go's GOARCH to Alpine's architecture naming.
func goarchToAlpine() string {
	switch runtime.GOARCH {
	case "arm64":
		return "aarch64"
	default:
		return runtime.GOARCH
	}
}

// download fetches the kernel and initrd for the current architecture
// from GitHub Releases. It tries the version-tagged release first,
// then falls back to the "images" tag used for image-only releases.
func download(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating image dir: %w", err)
	}

	arch := goarchToAlpine()
	tags := []string{"images-latest"}

	var lastErr error
	for _, tag := range tags {
		kernelURL := fmt.Sprintf("%s/%s/vmlinuz-%s", releaseBaseURL, tag, arch)
		initrdURL := fmt.Sprintf("%s/%s/initrd-%s", releaseBaseURL, tag, arch)

		fmt.Fprintf(os.Stderr, "pen: trying %s/%s ...\n", tag, arch)

		if err := downloadFile(kernelURL, filepath.Join(dir, kernelFile)); err != nil {
			lastErr = fmt.Errorf("downloading kernel from %s: %w", tag, err)
			continue
		}
		if err := downloadFile(initrdURL, filepath.Join(dir, initrdFile)); err != nil {
			// Clean up partial download.
			os.Remove(filepath.Join(dir, kernelFile))
			lastErr = fmt.Errorf("downloading initrd from %s: %w", tag, err)
			continue
		}

		fmt.Fprintf(os.Stderr, "pen: images downloaded to %s\n", dir)
		return nil
	}

	return fmt.Errorf("no release found: %w\n\nYou can build images locally with:\n  ./images/alpine/build.sh", lastErr)
}

// downloadFile fetches a URL to a local path. It writes to a temp file
// first to avoid leaving partial files on failure.
func downloadFile(url, dest string) error {
	resp, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}

	return os.Rename(tmp, dest)
}
