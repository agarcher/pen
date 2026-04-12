package envject

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteSetupFile(t *testing.T) {
	dir := t.TempDir()
	script := "echo hello\nexit 0\n"

	if err := WriteSetupFile(dir, script); err != nil {
		t.Fatalf("WriteSetupFile: %v", err)
	}

	path := filepath.Join(dir, SetupFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read setup file: %v", err)
	}
	if string(data) != script {
		t.Errorf("setup file contents = %q, want %q", string(data), script)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat setup file: %v", err)
	}
	if mode := st.Mode().Perm(); mode != 0600 {
		t.Errorf("setup file mode = %#o, want %#o", mode, 0600)
	}
}

func TestWriteSetupFileEmpty(t *testing.T) {
	cases := []string{"", "   ", "\n\n\t", "  \n  "}
	for _, s := range cases {
		dir := t.TempDir()
		if err := WriteSetupFile(dir, s); err != nil {
			t.Errorf("WriteSetupFile(%q): unexpected error: %v", s, err)
		}
		if _, err := os.Stat(filepath.Join(dir, SetupFileName)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("WriteSetupFile(%q): file was created (should be no-op)", s)
		}
	}
}

func TestWriteSetupFileSymlinkRefused(t *testing.T) {
	dir := t.TempDir()

	// Place a sentinel target that should NOT be clobbered.
	sentinel := filepath.Join(t.TempDir(), "sentinel")
	original := "do-not-touch"
	if err := os.WriteFile(sentinel, []byte(original), 0644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	// Pre-create .pen-setup as a symlink pointing at the sentinel.
	if err := os.Symlink(sentinel, filepath.Join(dir, SetupFileName)); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	err := WriteSetupFile(dir, "rm -rf /")
	if err == nil {
		t.Fatal("WriteSetupFile: expected error when .pen-setup is a symlink, got nil")
	}

	// Sentinel must be untouched.
	data, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if string(data) != original {
		t.Errorf("sentinel was clobbered: got %q, want %q", string(data), original)
	}
}

func TestWriteSetupFileRefusesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SetupFileName)
	if err := os.WriteFile(path, []byte("existing"), 0600); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	err := WriteSetupFile(dir, "new")
	if err == nil {
		t.Fatal("WriteSetupFile: expected error when file already exists")
	}
	// Must not have clobbered the existing file.
	data, _ := os.ReadFile(path)
	if string(data) != "existing" {
		t.Errorf("existing file was clobbered: got %q", string(data))
	}
}

func TestCleanupSetupFile(t *testing.T) {
	dir := t.TempDir()

	// Idempotent on missing file.
	CleanupSetupFile(dir)

	// Create then cleanup.
	if err := WriteSetupFile(dir, "echo hi"); err != nil {
		t.Fatalf("WriteSetupFile: %v", err)
	}
	CleanupSetupFile(dir)
	if _, err := os.Stat(filepath.Join(dir, SetupFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("CleanupSetupFile: file still exists after cleanup")
	}
}

func TestWriteSetupFilePreservesNewlines(t *testing.T) {
	// A TOML multiline string like `setup = """\n  echo hi\n"""` decodes
	// to "  echo hi\n"; we want WriteSetupFile to pass it through
	// verbatim so the guest's `sh` sees identical bytes.
	dir := t.TempDir()
	script := "  echo first\n  echo second\n"
	if err := WriteSetupFile(dir, script); err != nil {
		t.Fatalf("WriteSetupFile: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, SetupFileName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != script {
		t.Errorf("contents differ:\n got %q\nwant %q", string(data), script)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("trailing newline not preserved")
	}
}
