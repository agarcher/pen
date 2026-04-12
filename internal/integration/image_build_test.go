//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestImageBuildProducesUsableInitrd is the Phase 3 builder VM smoke
// test. It creates a profile with packages = ["jq"], builds the custom
// image via `pen image build`, then boots a VM with `pen shell --profile`
// and verifies that `jq` is available without any runtime apk add.
func TestImageBuildProducesUsableInitrd(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("integration tests require macOS + Apple Virtualization.framework")
	}

	pen := penBinary(t)
	ensureImages(t)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}

	// Create a unique profile.
	profileDir := filepath.Join(home, ".config", "pen", "profiles")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatalf("mkdir profiles: %v", err)
	}
	profileName := fmt.Sprintf("pen-integ-build-%d", time.Now().UnixNano())
	profilePath := filepath.Join(profileDir, profileName+".toml")

	body := `packages = ["jq"]
`
	if err := os.WriteFile(profilePath, []byte(body), 0644); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	t.Cleanup(func() {
		os.Remove(profilePath)
		cleanupProfileImage(t, profileName)
	})

	// Build the image.
	t.Logf("building image for profile %s", profileName)
	buildOut := runPenImageBuild(t, pen, profileName)
	t.Logf("build output:\n%s", buildOut)

	// Verify the cached initrd exists.
	imgDir := filepath.Join(home, ".config", "pen", "images", "profiles", profileName)
	if _, err := os.Stat(filepath.Join(imgDir, "initrd")); err != nil {
		t.Fatalf("cached initrd not found after build: %v", err)
	}
	if _, err := os.Stat(filepath.Join(imgDir, "build.hash")); err != nil {
		t.Fatalf("build.hash not found after build: %v", err)
	}

	// Boot a VM with the profile and verify jq is present.
	name := fmt.Sprintf("pen-integ-build-%d", time.Now().UnixNano())
	workspace := t.TempDir()
	t.Cleanup(func() {
		cleanupVM(t, pen, name)
	})

	t.Logf("booting VM %s with profile %s", name, profileName)
	out := runPenShell(t, pen, name, workspace, "which jq", "--profile", profileName)

	if !strings.Contains(out, "/usr/bin/jq") {
		t.Fatalf("jq not found in custom image — expected /usr/bin/jq in output:\n%s", out)
	}
}

// TestImageCacheInvalidation verifies that changing packages triggers a
// rebuild (hash changes) while changing setup does not (hash unchanged).
func TestImageCacheInvalidation(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("integration tests require macOS + Apple Virtualization.framework")
	}

	pen := penBinary(t)
	ensureImages(t)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}

	profileDir := filepath.Join(home, ".config", "pen", "profiles")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatalf("mkdir profiles: %v", err)
	}
	profileName := fmt.Sprintf("pen-integ-cache-%d", time.Now().UnixNano())
	profilePath := filepath.Join(profileDir, profileName+".toml")
	t.Cleanup(func() {
		os.Remove(profilePath)
		cleanupProfileImage(t, profileName)
	})

	imgDir := filepath.Join(home, ".config", "pen", "images", "profiles", profileName)

	// Build 1: packages = ["jq"]
	if err := os.WriteFile(profilePath, []byte(`packages = ["jq"]`+"\n"), 0644); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	t.Logf("build 1: packages = [jq]")
	runPenImageBuild(t, pen, profileName)

	hash1, err := os.ReadFile(filepath.Join(imgDir, "build.hash"))
	if err != nil {
		t.Fatalf("read build.hash after build 1: %v", err)
	}

	// Build 2: packages = ["jq", "curl"] — hash must change.
	if err := os.WriteFile(profilePath, []byte(`packages = ["jq", "curl"]`+"\n"), 0644); err != nil {
		t.Fatalf("rewrite profile: %v", err)
	}
	t.Logf("build 2: packages = [jq, curl]")
	runPenImageBuild(t, pen, profileName)

	hash2, err := os.ReadFile(filepath.Join(imgDir, "build.hash"))
	if err != nil {
		t.Fatalf("read build.hash after build 2: %v", err)
	}
	if strings.TrimSpace(string(hash1)) == strings.TrimSpace(string(hash2)) {
		t.Fatalf("build 2: hash did not change after adding a package")
	}

	// Build 3: add setup (no package change) — hash must NOT change.
	body3 := "packages = [\"jq\", \"curl\"]\nsetup = \"echo hello\"\n"
	if err := os.WriteFile(profilePath, []byte(body3), 0644); err != nil {
		t.Fatalf("rewrite profile: %v", err)
	}
	t.Logf("build 3: same packages + setup added")
	out3 := runPenImageBuild(t, pen, profileName)

	hash3, err := os.ReadFile(filepath.Join(imgDir, "build.hash"))
	if err != nil {
		t.Fatalf("read build.hash after build 3: %v", err)
	}
	if strings.TrimSpace(string(hash2)) != strings.TrimSpace(string(hash3)) {
		t.Fatalf("build 3: hash changed after only adding setup (should be cache hit)")
	}
	if !strings.Contains(out3, "up to date") {
		t.Fatalf("build 3: expected cache hit message, got:\n%s", out3)
	}
}

// runPenImageBuild runs `pen image build <profile>` and returns the
// combined output. Build VMs can take longer than shell boots (apk
// downloads + cpio repack), so we use a 120s timeout.
func runPenImageBuild(t *testing.T, pen, profileName string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, pen, "image", "build", profileName)
	out, err := cmd.CombinedOutput()
	t.Logf("--- pen image build %s output (%d bytes) ---\n%s--- end output ---",
		profileName, len(out), out)

	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("pen image build %s: timed out after 120s", profileName)
	}
	if err != nil {
		t.Fatalf("pen image build %s: %v", profileName, err)
	}
	return string(out)
}

// cleanupProfileImage removes the cached image directory for a profile.
func cleanupProfileImage(t *testing.T, profileName string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".config", "pen", "images", "profiles", profileName)
	if err := os.RemoveAll(dir); err != nil {
		t.Logf("cleanup: remove profile image %s: %v", dir, err)
	}
}
