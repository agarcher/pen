package vm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseDiskSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"10G", 10 * 1024 * 1024 * 1024, true},
		{"10g", 10 * 1024 * 1024 * 1024, true},
		{"10GB", 10 * 1024 * 1024 * 1024, true},
		{"10GiB", 10 * 1024 * 1024 * 1024, true},
		{"512M", 512 * 1024 * 1024, true},
		{"2K", 2 * 1024, true},
		{"1T", 1024 * 1024 * 1024 * 1024, true},
		{"2048", 2048, true},
		{"  4G ", 4 * 1024 * 1024 * 1024, true},
		{"", 0, false},
		{"0G", 0, false},
		{"-1G", 0, false},
		{"10X", 0, false},
		{"abc", 0, false},
	}
	for _, c := range cases {
		got, err := ParseDiskSize(c.in)
		if c.ok {
			if err != nil {
				t.Errorf("ParseDiskSize(%q) unexpected error: %v", c.in, err)
				continue
			}
			if got != c.want {
				t.Errorf("ParseDiskSize(%q) = %d, want %d", c.in, got, c.want)
			}
		} else {
			if err == nil {
				t.Errorf("ParseDiskSize(%q) = %d, want error", c.in, got)
			}
		}
	}
}

func TestEnsureOverlay(t *testing.T) {
	// Redirect HOME so the helper writes into a temp dir.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	const name = "test-vm"
	const size int64 = 4 * 1024 * 1024 // 4 MiB

	path, err := EnsureOverlay(name, size)
	if err != nil {
		t.Fatalf("EnsureOverlay: %v", err)
	}

	wantPath := filepath.Join(tmp, ".config", "pen", "vms", name, "overlay.img")
	if path != wantPath {
		t.Errorf("path = %q, want %q", path, wantPath)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat overlay: %v", err)
	}
	if st.Size() != size {
		t.Errorf("file size = %d, want %d", st.Size(), size)
	}

	// Second call should be a no-op and must not resize the existing file
	// (overlay disks are sized once at creation time).
	path2, err := EnsureOverlay(name, 99*1024*1024)
	if err != nil {
		t.Fatalf("EnsureOverlay (second call): %v", err)
	}
	if path2 != path {
		t.Errorf("second path = %q, want %q", path2, path)
	}
	st2, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat overlay (second): %v", err)
	}
	if st2.Size() != size {
		t.Errorf("file size after second call = %d, want %d (must not resize)", st2.Size(), size)
	}
}
