package image

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestProfileImageHashDeterministic(t *testing.T) {
	initrd := filepath.Join(t.TempDir(), "initrd")
	writeTestFile(t, initrd, "base-initrd-content")

	h1, err := ProfileImageHash([]string{"nodejs", "npm"}, "echo build", initrd)
	if err != nil {
		t.Fatalf("hash 1: %v", err)
	}
	h2, err := ProfileImageHash([]string{"nodejs", "npm"}, "echo build", initrd)
	if err != nil {
		t.Fatalf("hash 2: %v", err)
	}
	if h1 != h2 {
		t.Errorf("same inputs produced different hashes: %s vs %s", h1, h2)
	}
}

func TestProfileImageHashPackageOrderIndependent(t *testing.T) {
	initrd := filepath.Join(t.TempDir(), "initrd")
	writeTestFile(t, initrd, "base-initrd-content")

	h1, err := ProfileImageHash([]string{"npm", "nodejs"}, "", initrd)
	if err != nil {
		t.Fatalf("hash 1: %v", err)
	}
	h2, err := ProfileImageHash([]string{"nodejs", "npm"}, "", initrd)
	if err != nil {
		t.Fatalf("hash 2: %v", err)
	}
	if h1 != h2 {
		t.Errorf("different package order produced different hashes: %s vs %s", h1, h2)
	}
}

func TestProfileImageHashChangesWithPackages(t *testing.T) {
	initrd := filepath.Join(t.TempDir(), "initrd")
	writeTestFile(t, initrd, "base-initrd-content")

	h1, err := ProfileImageHash([]string{"nodejs"}, "", initrd)
	if err != nil {
		t.Fatalf("hash 1: %v", err)
	}
	h2, err := ProfileImageHash([]string{"nodejs", "npm"}, "", initrd)
	if err != nil {
		t.Fatalf("hash 2: %v", err)
	}
	if h1 == h2 {
		t.Error("different packages produced same hash")
	}
}

func TestProfileImageHashChangesWithBuild(t *testing.T) {
	initrd := filepath.Join(t.TempDir(), "initrd")
	writeTestFile(t, initrd, "base-initrd-content")

	h1, err := ProfileImageHash([]string{"nodejs"}, "echo v1", initrd)
	if err != nil {
		t.Fatalf("hash 1: %v", err)
	}
	h2, err := ProfileImageHash([]string{"nodejs"}, "echo v2", initrd)
	if err != nil {
		t.Fatalf("hash 2: %v", err)
	}
	if h1 == h2 {
		t.Error("different build scripts produced same hash")
	}
}

func TestProfileImageHashChangesWithBaseInitrd(t *testing.T) {
	dir := t.TempDir()
	initrd1 := filepath.Join(dir, "initrd1")
	initrd2 := filepath.Join(dir, "initrd2")
	writeTestFile(t, initrd1, "base-v1")
	writeTestFile(t, initrd2, "base-v2")

	h1, err := ProfileImageHash([]string{"nodejs"}, "", initrd1)
	if err != nil {
		t.Fatalf("hash 1: %v", err)
	}
	h2, err := ProfileImageHash([]string{"nodejs"}, "", initrd2)
	if err != nil {
		t.Fatalf("hash 2: %v", err)
	}
	if h1 == h2 {
		t.Error("different base initrds produced same hash")
	}
}

func TestIsImageFreshMissingDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fresh, err := IsImageFresh("nonexistent", "abc123")
	if err != nil {
		t.Fatalf("IsImageFresh: %v", err)
	}
	if fresh {
		t.Error("missing dir should not be fresh")
	}
}

func TestIsImageFreshMissingHash(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir, err := ProfileImageDir("test")
	if err != nil {
		t.Fatalf("ProfileImageDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write initrd but no hash file.
	writeTestFile(t, filepath.Join(dir, "initrd"), "fake-initrd")

	fresh, err := IsImageFresh("test", "abc123")
	if err != nil {
		t.Fatalf("IsImageFresh: %v", err)
	}
	if fresh {
		t.Error("missing hash file should not be fresh")
	}
}

func TestIsImageFreshMismatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir, err := ProfileImageDir("test")
	if err != nil {
		t.Fatalf("ProfileImageDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeTestFile(t, filepath.Join(dir, "initrd"), "fake-initrd")
	writeTestFile(t, filepath.Join(dir, "build.hash"), "oldhash")

	fresh, err := IsImageFresh("test", "newhash")
	if err != nil {
		t.Fatalf("IsImageFresh: %v", err)
	}
	if fresh {
		t.Error("hash mismatch should not be fresh")
	}
}

func TestIsImageFreshMatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir, err := ProfileImageDir("test")
	if err != nil {
		t.Fatalf("ProfileImageDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeTestFile(t, filepath.Join(dir, "initrd"), "fake-initrd")
	writeTestFile(t, filepath.Join(dir, "build.hash"), "correcthash\n")

	fresh, err := IsImageFresh("test", "correcthash")
	if err != nil {
		t.Fatalf("IsImageFresh: %v", err)
	}
	if !fresh {
		t.Error("matching hash should be fresh")
	}
}
