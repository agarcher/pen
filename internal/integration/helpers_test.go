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

// bootTimeout bounds a single `pen shell` invocation. On a fresh VM the
// first boot has to DHCP, apk update + apk add e2fsprogs, mkfs the disk,
// and (for profile-setup tests) run the profile's setup script — keep
// this generous enough that a slow mirror doesn't flake the test but
// tight enough that a genuine hang fails fast.
const bootTimeout = 5 * time.Minute

// runPenShell spawns `pen shell NAME --dir WORKSPACE [extra...]`, feeds
// it the provided shell script on stdin followed by `exit`, waits for
// clean completion, and returns the combined stdout+stderr.
//
// Any error (non-zero exit, timeout, i/o error) is fatal: the boot log
// is always logged via t.Logf so the caller can see what happened.
func runPenShell(t *testing.T, pen, name, workspace, script string, extra ...string) string {
	t.Helper()

	if !strings.HasSuffix(script, "\n") {
		script += "\n"
	}
	// The guest init runs `/bin/sh -l` as a child and then `poweroff -f`
	// when the shell exits, so we just need to exit the shell to get a
	// clean shutdown. Sending EOF (closing stdin) would also work but
	// an explicit `exit` is clearer in the logged script.
	script += "exit\n"

	ctx, cancel := context.WithTimeout(context.Background(), bootTimeout)
	defer cancel()

	args := append([]string{"shell", name, "--dir", workspace}, extra...)
	cmd := exec.CommandContext(ctx, pen, args...)
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()

	// Always log the boot output so -v runs can diagnose failures
	// without having to re-run.
	t.Logf("--- pen shell %s args=%v stdin ---\n%s--- pen shell %s output (%d bytes) ---\n%s--- end output ---",
		name, args, script, name, len(out), out)

	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("pen shell %s: timed out after %s", name, bootTimeout)
	}
	if err != nil {
		t.Fatalf("pen shell %s: exec failed: %v", name, err)
	}
	return string(out)
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
