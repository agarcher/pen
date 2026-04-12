//go:build integration

package integration_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestProfileSetupIdempotency is the Phase 2 integration test from
// plans/profiles-and-overlay.md:
//
//	Test: first `pen shell --profile foo bar` runs setup; second run doesn't;
//	      editing the profile doesn't re-trigger.
//
// Three boots against the same VM name:
//
//  1. Fresh VM, profile with setup that echoes INTEG_SETUP_MARKER_ALPHA
//     and writes /root/setup-evidence. Assert the marker appears in
//     output (setup ran) and the evidence file exists.
//  2. Same VM, same profile. Assert INTEG_SETUP_MARKER_ALPHA does NOT
//     appear (marker prevented re-run) but /root/setup-evidence is
//     still present (overlay persistence).
//  3. Edit the profile between boots to echo INTEG_SETUP_MARKER_BETA.
//     Boot again. Assert BETA does NOT appear — editing the profile
//     does not retrigger setup on an existing VM.
func TestProfileSetupIdempotency(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("integration tests require macOS + Apple Virtualization.framework")
	}

	pen := penBinary(t)
	ensureImages(t)

	// Create a unique profile under the real ~/.config/pen/profiles/
	// directory so the pen binary (which resolves profile paths from
	// $HOME) can find it. The per-run timestamp keeps concurrent runs
	// isolated and a t.Cleanup removes the file when done.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	profileDir := filepath.Join(home, ".config", "pen", "profiles")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatalf("mkdir profiles: %v", err)
	}
	profileName := fmt.Sprintf("pen-integ-setup-%d", time.Now().UnixNano())
	profilePath := filepath.Join(profileDir, profileName+".toml")

	alphaBody := `setup = """
echo INTEG_SETUP_MARKER_ALPHA
echo setup-ran > /root/setup-evidence
"""
`
	if err := os.WriteFile(profilePath, []byte(alphaBody), 0644); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	t.Cleanup(func() {
		os.Remove(profilePath)
	})

	name := fmt.Sprintf("pen-integ-setup-%d", time.Now().UnixNano())
	workspace := t.TempDir()
	t.Cleanup(func() {
		cleanupVM(t, pen, name)
	})

	// --- Boot 1: fresh VM, setup should run ---
	//
	// Marker-string strategy matches TestOverlayPersistence: we only
	// assert on strings that can only appear as genuine command output,
	// never as part of a command line that busybox sh would echo back.
	//
	//   - "INTEG_SETUP_MARKER_ALPHA" appears only in `echo` output from
	//     *inside* the setup script (which runs under a forked `sh`,
	//     whose command echo doesn't reach the parent console). The
	//     setup hook also prefixes every line with "pen-setup:", which
	//     makes the matched string "pen-setup: INTEG_SETUP_MARKER_ALPHA".
	//   - "setup-ran" appears only as the content of /root/setup-evidence
	//     emitted by `cat` — the command line is `cat /root/setup-evidence`
	//     so "setup-ran" itself is unambiguous output.
	//   - "setup-done" appears only as an `ls /var/lib/pen` output entry.
	t.Logf("boot 1: VM=%s profile=%s workspace=%s", name, profileName, workspace)
	out1 := runPenShell(t, pen, name, workspace, strings.Join([]string{
		`ls /var/lib/pen/`,
		`cat /root/setup-evidence`,
	}, "\n"), "--profile", profileName)

	if !strings.Contains(out1, "INTEG_SETUP_MARKER_ALPHA") {
		t.Fatalf("boot 1: setup script did not run (marker absent)")
	}
	if !strings.Contains(out1, "setup-done") {
		t.Fatalf("boot 1: /var/lib/pen/setup-done marker not written")
	}
	if !strings.Contains(out1, "setup-ran") {
		t.Fatalf("boot 1: /root/setup-evidence not populated by setup")
	}

	// --- Boot 2: same VM, setup should NOT re-run ---
	t.Logf("boot 2: VM=%s (idempotency check)", name)
	out2 := runPenShell(t, pen, name, workspace, strings.Join([]string{
		`ls /var/lib/pen/`,
		`cat /root/setup-evidence`,
	}, "\n"), "--profile", profileName)

	if strings.Contains(out2, "INTEG_SETUP_MARKER_ALPHA") {
		t.Fatalf("boot 2: setup re-ran (marker file did not prevent re-execution)")
	}
	if !strings.Contains(out2, "setup-done") {
		t.Fatalf("boot 2: /var/lib/pen/setup-done marker missing (overlay lost?)")
	}
	if !strings.Contains(out2, "setup-ran") {
		t.Fatalf("boot 2: /root/setup-evidence vanished (overlay lost?)")
	}

	// --- Edit the profile between boots ---
	betaBody := `setup = """
echo INTEG_SETUP_MARKER_BETA
echo beta-ran > /root/setup-evidence
"""
`
	if err := os.WriteFile(profilePath, []byte(betaBody), 0644); err != nil {
		t.Fatalf("rewrite profile: %v", err)
	}

	// --- Boot 3: edited profile must NOT retrigger setup ---
	t.Logf("boot 3: VM=%s (edited-profile check)", name)
	out3 := runPenShell(t, pen, name, workspace, strings.Join([]string{
		`cat /root/setup-evidence`,
	}, "\n"), "--profile", profileName)

	if strings.Contains(out3, "INTEG_SETUP_MARKER_BETA") {
		t.Fatalf("boot 3: edited profile retriggered setup on existing VM")
	}
	if strings.Contains(out3, "beta-ran") {
		t.Fatalf("boot 3: /root/setup-evidence was overwritten by edited setup")
	}
	if !strings.Contains(out3, "setup-ran") {
		t.Fatalf("boot 3: original setup-evidence lost")
	}
}
