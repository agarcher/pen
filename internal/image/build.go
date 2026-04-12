package image

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/agarcher/pen/internal/virt"
)

// Build builds a custom image for the named profile. It boots a builder
// VM using the base kernel+initrd, installs packages, runs the build
// script, and repacks the rootfs into a new initrd cached at
// ~/.config/pen/images/profiles/<name>/initrd.
//
// If the cached image is already fresh (hash matches), this is a no-op.
// Build progress is streamed to w.
func Build(hyp virt.Hypervisor, profileName string, packages []string, buildScript string, basePaths *Paths, w io.Writer) error {
	expectedHash, err := ProfileImageHash(packages, buildScript, basePaths.Initrd)
	if err != nil {
		return fmt.Errorf("computing image hash: %w", err)
	}

	fresh, err := IsImageFresh(profileName, expectedHash)
	if err != nil {
		return fmt.Errorf("checking image freshness: %w", err)
	}
	if fresh {
		fmt.Fprintf(w, "pen: image for profile %q is up to date\n", profileName)
		return nil
	}

	fmt.Fprintf(w, "pen: building image for profile %q...\n", profileName)

	// Create temp directory with control/ and output/ subdirs.
	tmpDir, err := os.MkdirTemp("", "pen-build-"+profileName+"-")
	if err != nil {
		return fmt.Errorf("creating build temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	controlDir := filepath.Join(tmpDir, "control")
	outputDir := filepath.Join(tmpDir, "output")
	if err := os.Mkdir(controlDir, 0755); err != nil {
		return fmt.Errorf("creating control dir: %w", err)
	}
	if err := os.Mkdir(outputDir, 0755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	// Write control/packages (newline-separated).
	if len(packages) > 0 {
		pkgContent := strings.Join(packages, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(controlDir, "packages"), []byte(pkgContent), 0644); err != nil {
			return fmt.Errorf("writing packages file: %w", err)
		}
	}

	// Write control/build.sh.
	if strings.TrimSpace(buildScript) != "" {
		if err := os.WriteFile(filepath.Join(controlDir, "build.sh"), []byte(buildScript), 0644); err != nil {
			return fmt.Errorf("writing build script: %w", err)
		}
	}

	// Boot the builder VM.
	v, err := hyp.CreateVM(virt.VMConfig{
		Name:       "pen-build-" + profileName,
		KernelPath: basePaths.Kernel,
		InitrdPath: basePaths.Initrd,
		CmdLine:    "console=hvc0 pen.mode=build",
		CPUs:       uint(runtime.NumCPU()),
		MemoryMB:   2048,
		Shares: []virt.Share{
			{HostPath: controlDir, Tag: "control", ReadOnly: true},
			{HostPath: outputDir, Tag: "output", ReadOnly: false},
		},
	})
	if err != nil {
		return fmt.Errorf("creating builder VM: %w", err)
	}

	if err := v.Start(); err != nil {
		return fmt.Errorf("starting builder VM: %w", err)
	}

	// Stream console output to the caller.
	consoleReader, _ := v.Console()
	done := make(chan struct{})
	go func() {
		io.Copy(w, consoleReader)
		close(done)
	}()

	// Wait for the builder VM to halt.
	if err := v.Wait(); err != nil {
		return fmt.Errorf("builder VM failed: %w", err)
	}
	<-done

	// Verify the output initrd was produced.
	outputInitrd := filepath.Join(outputDir, "initrd")
	info, err := os.Stat(outputInitrd)
	if err != nil {
		return fmt.Errorf("build failed: no output initrd produced (check build log above)")
	}
	if info.Size() == 0 {
		return fmt.Errorf("build failed: output initrd is empty (check build log above)")
	}

	// Move to the profile image cache.
	profileDir, err := ProfileImageDir(profileName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		return fmt.Errorf("creating profile image dir: %w", err)
	}

	destInitrd := filepath.Join(profileDir, initrdFile)
	// Copy rather than rename — tmp and profile dir may be on different filesystems.
	if err := copyFile(outputInitrd, destInitrd); err != nil {
		return fmt.Errorf("storing built initrd: %w", err)
	}

	// Write the hash file.
	if err := os.WriteFile(filepath.Join(profileDir, hashFile), []byte(expectedHash+"\n"), 0644); err != nil {
		return fmt.Errorf("writing build hash: %w", err)
	}

	fmt.Fprintf(w, "pen: image built for profile %q (%d bytes)\n", profileName, info.Size())
	return nil
}

// EnsureFresh builds the profile image if stale and returns paths
// pointing to the base kernel and the profile's custom initrd.
func EnsureFresh(hyp virt.Hypervisor, profileName string, packages []string, buildScript string, basePaths *Paths, w io.Writer) (*Paths, error) {
	if err := Build(hyp, profileName, packages, buildScript, basePaths, w); err != nil {
		return nil, err
	}

	profileDir, err := ProfileImageDir(profileName)
	if err != nil {
		return nil, err
	}

	return &Paths{
		Kernel: basePaths.Kernel,
		Initrd: filepath.Join(profileDir, initrdFile),
	}, nil
}

// copyFile copies src to dst, creating dst with the same permissions.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}
