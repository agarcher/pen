package profile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProfile is a test helper that creates a profile file under the
// temp HOME's profiles dir and returns the profile base name.
func writeProfile(t *testing.T, name, body string) {
	t.Helper()
	dir, err := Dir()
	if err != nil {
		t.Fatalf("profile.Dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir profiles: %v", err)
	}
	path := filepath.Join(dir, name+fileExt)
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func TestLoadValid(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	body := `
packages = ["nodejs", "npm", "git", "ripgrep"]
build = """
npm install -g @anthropic-ai/claude-code
rm -rf /var/cache/apk/*
"""
setup = """
mkdir -p /root/.claude
"""

[disk]
size = "10G"
`
	writeProfile(t, "claude", body)

	p, err := Load("claude")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Name != "claude" {
		t.Errorf("Name = %q, want %q", p.Name, "claude")
	}
	wantPkgs := []string{"nodejs", "npm", "git", "ripgrep"}
	if len(p.Packages) != len(wantPkgs) {
		t.Fatalf("Packages len = %d, want %d", len(p.Packages), len(wantPkgs))
	}
	for i, pkg := range wantPkgs {
		if p.Packages[i] != pkg {
			t.Errorf("Packages[%d] = %q, want %q", i, p.Packages[i], pkg)
		}
	}
	if !strings.Contains(p.Build, "npm install -g @anthropic-ai/claude-code") {
		t.Errorf("Build did not contain npm install: %q", p.Build)
	}
	if !strings.Contains(p.Setup, "mkdir -p /root/.claude") {
		t.Errorf("Setup did not contain mkdir: %q", p.Setup)
	}
	if p.Disk.Size != "10G" {
		t.Errorf("Disk.Size = %q, want %q", p.Disk.Size, "10G")
	}
}

func TestLoadSetupOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	body := `setup = "echo hello"` + "\n"
	writeProfile(t, "tiny", body)

	p, err := Load("tiny")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(p.Packages) != 0 {
		t.Errorf("Packages len = %d, want 0", len(p.Packages))
	}
	if p.Build != "" {
		t.Errorf("Build = %q, want empty", p.Build)
	}
	if strings.TrimSpace(p.Setup) != "echo hello" {
		t.Errorf("Setup = %q, want %q", p.Setup, "echo hello")
	}
}

func TestLoadNotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := Load("nope")
	if err == nil {
		t.Fatal("Load(nope): expected error, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load(nope): error is not os.ErrNotExist: %v", err)
	}
}

func TestLoadUnknownKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// "packagess" is a typo for "packages" — must fail loudly.
	writeProfile(t, "typo", `packagess = ["nodejs"]`+"\n")

	_, err := Load("typo")
	if err == nil {
		t.Fatal("Load(typo): expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown keys") {
		t.Errorf("Load(typo): error %q does not mention unknown keys", err.Error())
	}
	if !strings.Contains(err.Error(), "packagess") {
		t.Errorf("Load(typo): error %q does not name the unknown key", err.Error())
	}
}

func TestLoadInvalidName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cases := []string{
		"",
		"..",
		".hidden",
		"foo/bar",
		"-leading-dash",
		"with space",
		"with\nnewline",
		strings.Repeat("a", maxNameLen+1),
	}
	for _, name := range cases {
		_, err := Load(name)
		if err == nil {
			t.Errorf("Load(%q): expected error, got nil", name)
		}
	}
}

func TestLoadInvalidPackageName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	writeProfile(t, "bad-pkgs", `packages = ["Foo Bar"]`+"\n")
	_, err := Load("bad-pkgs")
	if err == nil {
		t.Fatal("expected error for uppercase + space package name")
	}
	if !strings.Contains(err.Error(), "invalid package name") {
		t.Errorf("error %q does not mention invalid package name", err.Error())
	}
}

func TestLoadInvalidDiskSize(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	writeProfile(t, "bad-disk", "[disk]\nsize = \"potato\"\n")
	_, err := Load("bad-disk")
	if err == nil {
		t.Fatal("expected error for bad disk size")
	}
	if !strings.Contains(err.Error(), "disk.size") {
		t.Errorf("error %q does not mention disk.size", err.Error())
	}
}

func TestList(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	writeProfile(t, "alpha", `setup = "echo alpha"`+"\n")
	writeProfile(t, "beta", `setup = "echo beta"`+"\n")
	writeProfile(t, "gamma", `setup = "echo gamma"`+"\n")
	// A broken file should be surfaced in perFileErrs but must not
	// stop the healthy ones from being listed.
	writeProfile(t, "broken", `packagess = ["nope"]`+"\n")
	// Non-TOML files in the dir should be ignored silently.
	dir, _ := Dir()
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hi"), 0644); err != nil {
		t.Fatalf("write readme: %v", err)
	}

	profiles, perFileErrs, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(profiles) != 3 {
		t.Errorf("profiles len = %d, want 3", len(profiles))
	}
	if len(perFileErrs) != 1 {
		t.Errorf("perFileErrs len = %d, want 1", len(perFileErrs))
	}
	// Sorted by name.
	wantNames := []string{"alpha", "beta", "gamma"}
	for i, want := range wantNames {
		if i >= len(profiles) {
			break
		}
		if profiles[i].Name != want {
			t.Errorf("profiles[%d].Name = %q, want %q", i, profiles[i].Name, want)
		}
	}
}

func TestNeedsImageBuild(t *testing.T) {
	cases := []struct {
		name     string
		packages []string
		build    string
		setup    string
		want     bool
	}{
		{"empty", nil, "", "", false},
		{"setup-only", nil, "", "echo hello", false},
		{"packages-only", []string{"nodejs"}, "", "", true},
		{"build-only", nil, "echo hello", "", true},
		{"both", []string{"nodejs"}, "echo hello", "", true},
		{"whitespace-build", nil, "  \n  ", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &Profile{Packages: tc.packages, Build: tc.build, Setup: tc.setup}
			if got := p.NeedsImageBuild(); got != tc.want {
				t.Errorf("NeedsImageBuild() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestListEmptyDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	profiles, perFileErrs, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(profiles) != 0 {
		t.Errorf("profiles len = %d, want 0", len(profiles))
	}
	if len(perFileErrs) != 0 {
		t.Errorf("perFileErrs len = %d, want 0", len(perFileErrs))
	}
}
