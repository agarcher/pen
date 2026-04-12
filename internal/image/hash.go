package image

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	profilesSubdir = "profiles"
	hashFile       = "build.hash"
)

// ProfileImageHash computes the cache key for a profile's custom image.
// It hashes the sorted package list, the build script, and the content
// of the base initrd. Sorting packages makes the hash order-independent.
func ProfileImageHash(packages []string, build string, baseInitrdPath string) (string, error) {
	h := sha256.New()

	// Hash sorted packages.
	sorted := make([]string, len(packages))
	copy(sorted, packages)
	sort.Strings(sorted)
	for _, pkg := range sorted {
		fmt.Fprintf(h, "pkg:%s\n", pkg)
	}

	// Hash build script.
	fmt.Fprintf(h, "build:%s\n", build)

	// Hash base initrd content.
	f, err := os.Open(baseInitrdPath)
	if err != nil {
		return "", fmt.Errorf("reading base initrd for hash: %w", err)
	}
	defer f.Close()

	initrdHash := sha256.New()
	if _, err := io.Copy(initrdHash, f); err != nil {
		return "", fmt.Errorf("hashing base initrd: %w", err)
	}
	fmt.Fprintf(h, "base:%s\n", hex.EncodeToString(initrdHash.Sum(nil)))

	return hex.EncodeToString(h.Sum(nil)), nil
}

// ProfileImageDir returns the directory where a profile's custom image
// is cached: ~/.config/pen/images/profiles/<name>/.
func ProfileImageDir(profileName string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, profilesSubdir, profileName), nil
}

// IsImageFresh checks whether the cached image for a profile matches
// the expected hash. Returns false if the directory, hash file, or
// initrd is missing, or if the hash doesn't match.
func IsImageFresh(profileName string, expectedHash string) (bool, error) {
	dir, err := ProfileImageDir(profileName)
	if err != nil {
		return false, err
	}

	// Both initrd and hash file must exist.
	if _, err := os.Stat(filepath.Join(dir, initrdFile)); err != nil {
		return false, nil
	}

	stored, err := os.ReadFile(filepath.Join(dir, hashFile))
	if err != nil {
		return false, nil
	}

	return strings.TrimSpace(string(stored)) == expectedHash, nil
}
