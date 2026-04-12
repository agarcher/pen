//go:build integration

package integration_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// bootTimeout bounds a single boot attempt. A successful boot (including
// first-boot DHCP + apk + mkfs + setup) completes in under 15s. A hung
// VM produces zero console output, so there's no point waiting longer
// than 30s — it's either working or completely dead.
const bootTimeout = 30 * time.Second

// maxBootRetries is the number of times to retry a boot that times out.
// Apple's Virtualization.framework occasionally fails to connect the
// serial console on VM start (~20% of boots), producing zero kernel
// output. This is a VZ bug — the VM is created and started without
// error but the console pipe is dead. Retrying is the only mitigation.
const maxBootRetries = 3

// runPenShell spawns `pen shell NAME --dir WORKSPACE [extra...]`, feeds
// it the provided shell script on stdin followed by `exit`, waits for
// clean completion, and returns the combined stdout+stderr.
//
// If a boot times out, it retries up to maxBootRetries times to work
// around a VZ framework bug where the serial console intermittently
// fails to connect. Non-timeout errors are always fatal.
func runPenShell(t *testing.T, pen, name, workspace, script string, extra ...string) string {
	t.Helper()

	if !strings.HasSuffix(script, "\n") {
		script += "\n"
	}
	script += "exit\n"

	args := append([]string{"shell", name, "--dir", workspace}, extra...)

	for attempt := 1; attempt <= maxBootRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), bootTimeout)

		cmd := exec.CommandContext(ctx, pen, args...)
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		cancel()

		t.Logf("--- pen shell %s attempt %d/%d args=%v stdin ---\n%s--- pen shell %s output (%d bytes) ---\n%s--- end output ---",
			name, attempt, maxBootRetries, args, script, name, len(out), out)

		if ctx.Err() != context.DeadlineExceeded {
			if err != nil {
				t.Fatalf("pen shell %s: exec failed: %v", name, err)
			}
			return string(out)
		}

		// Timed out. If we have retries left, clean up stale dotfiles
		// left by the killed process and try again. The killed pen
		// process's defer never ran, so .pen-env/.pen-setup may linger
		// in the workspace. We must remove them here because pen uses
		// O_EXCL to prevent concurrent boots on the same workspace —
		// a deliberate safety feature we don't want to bypass in prod.
		if attempt < maxBootRetries {
			t.Logf("pen shell %s: boot attempt %d timed out (VZ console flake), retrying", name, attempt)
			os.Remove(filepath.Join(workspace, ".pen-env"))
			os.Remove(filepath.Join(workspace, ".pen-setup"))
			continue
		}
		t.Fatalf("pen shell %s: all %d boot attempts timed out after %s each", name, maxBootRetries, bootTimeout)
	}
	panic("unreachable")
}

// penBinary resolves the path to the pen binary built by `make build`.
// Fails fast with a clear message if it isn't there — integration tests
// should not transparently rebuild the binary (that's the Makefile's job).
func penBinary(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	pen := filepath.Join(root, "build", "pen")
	if _, err := os.Stat(pen); err != nil {
		t.Fatalf("pen binary not found at %s — run `make build` first: %v", pen, err)
	}
	return pen
}

// ensureImages verifies kernel + initrd exist in the user image cache.
// We deliberately do NOT override $HOME in these tests — pen's image
// resolution reads from $HOME/.config/pen/images/ and re-downloading a
// 30 MB initrd on every test run would be prohibitive. Using a unique
// VM name per test keeps runs isolated from each other.
func ensureImages(t *testing.T) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	dir := filepath.Join(home, ".config", "pen", "images")
	for _, f := range []string{"vmlinuz", "initrd"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("image %s not found — run `make image` first: %v",
				filepath.Join(dir, f), err)
		}
	}
}

// repoRoot walks up from this test file's location to find the directory
// containing go.mod. It works regardless of where `go test` is invoked
// from (package dir, repo root, etc.).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod walking up from %s", filepath.Dir(file))
		}
		dir = parent
	}
}

// cleanupVM is a best-effort teardown that runs `pen delete NAME` so
// test state doesn't leak into the user's VM list. It does not fail the
// test if delete returns an error (the test may already be failing for
// a different reason, and we don't want to mask the original failure).
func cleanupVM(t *testing.T, pen, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, pen, "delete", name).CombinedOutput()
	if err != nil {
		t.Logf("cleanup: pen delete %s failed: %v\n%s", name, err, out)
	}
}
