//go:build integration

// Package integration holds black-box integration tests that exercise the
// full pen binary against a real Apple Virtualization.framework VM.
//
// These tests are gated on the `integration` build tag and require:
//   - macOS + Apple Silicon or Intel with Virtualization.framework
//   - `make build` — the pen binary at <repo>/build/pen
//   - `make image` — kernel + initrd at $HOME/.config/pen/images/
//
// They talk to the real user image cache under $HOME/.config/pen/images/
// rather than a temp HOME: re-downloading or rebuilding a 30 MB initrd
// for every test run would be prohibitive, and the test always uses a
// unique VM name plus a t.Cleanup to remove its state when done.
package integration_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestOverlayPersistence is the Phase 1 integration test from
// plans/profiles-and-overlay.md:
//
//	Test: `apk add vim`, exit, `pen shell`, verify `vim` still present.
//
// It runs two pen-shell invocations against the same VM name, with a
// unique per-run suffix so parallel or repeated runs can't collide.
// The first boot installs vim via apk; the second asserts the binary
// is still at /usr/bin/vim, which is only possible if the overlay disk
// persisted the apk install across the VM reboot.
func TestOverlayPersistence(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("integration tests require macOS + Apple Virtualization.framework")
	}

	pen := penBinary(t)
	ensureImages(t)

	name := fmt.Sprintf("pen-integ-overlay-%d", time.Now().UnixNano())
	workspace := t.TempDir()

	t.Cleanup(func() {
		cleanupVM(t, pen, name)
	})

	// --- Boot 1: fresh VM, install vim ---
	//
	// Assertion strategy: busybox /bin/sh running on a non-tty console
	// echoes every command line it reads back to stdout, so any unique
	// marker string we put *in* a command (e.g. `|| echo VIM_MISSING`)
	// also appears in the output as part of the command echo. We avoid
	// negative markers entirely and only look for strings that can only
	// appear as genuine command *output*:
	//
	//   - "Installing vim" appears in apk's output, never in a command line.
	//   - "/usr/bin/vim" appears in `which vim`'s output, never in a
	//     command line (the command itself is `which vim`, no path).
	//   - "overlay on /" appears in `mount`'s output, never in a command line.
	t.Logf("boot 1: VM=%s workspace=%s", name, workspace)
	out1 := runPenShell(t, pen, name, workspace, strings.Join([]string{
		`mount | grep -E '(overlay|vda)'`,
		`apk add vim`,
		`which vim`,
	}, "\n"))

	if !strings.Contains(out1, "overlay on /") {
		t.Fatalf("boot 1: overlayfs was not mounted at / — overlay disk setup failed")
	}
	if !strings.Contains(out1, "Installing vim") {
		t.Fatalf("boot 1: `apk add vim` did not run to completion (network? mirror?)")
	}
	if !strings.Contains(out1, "/usr/bin/vim") {
		t.Fatalf("boot 1: `which vim` did not return /usr/bin/vim after install")
	}

	// --- Boot 2: same VM, vim must still be there ---
	t.Logf("boot 2: VM=%s (persistence check)", name)
	out2 := runPenShell(t, pen, name, workspace, strings.Join([]string{
		`mount | grep -E '(overlay|vda)'`,
		`which vim`,
	}, "\n"))

	if !strings.Contains(out2, "overlay on /") {
		t.Fatalf("boot 2: overlayfs not mounted — upper layer not restored")
	}
	if !strings.Contains(out2, "/usr/bin/vim") {
		t.Fatalf("boot 2: vim did NOT persist across reboot — `which vim` returned nothing")
	}
}
