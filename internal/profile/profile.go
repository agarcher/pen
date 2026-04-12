// Package profile loads and validates pen VM profiles from
// ~/.config/pen/profiles/<name>.toml.
//
// A profile describes how to build a custom image (packages + build)
// and what to run on the first boot of a fresh VM (setup).
package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/agarcher/pen/internal/vm"
)

const (
	configDir   = ".config/pen"
	profilesDir = "profiles"
	fileExt     = ".toml"
	maxNameLen  = 64
)

// nameRE matches valid profile names: starts with an alphanumeric, then
// alphanumerics, underscores, or dashes. Rejects "..", slashes, leading
// dots or dashes.
var nameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// packageRE matches valid Alpine package names (the charset we accept;
// existence is not checked — that's a Phase 3 build-time concern).
var packageRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._+-]*$`)

// Profile is a parsed, validated profile. Name is derived from the
// filename and is not a TOML field.
type Profile struct {
	Name     string   `toml:"-"`
	Packages []string `toml:"packages"`
	Build    string   `toml:"build"`
	Setup    string   `toml:"setup"`
	Disk     Disk     `toml:"disk"`
}

// Disk describes the overlay-disk options for a profile.
type Disk struct {
	Size string `toml:"size"`
}

// NeedsImageBuild reports whether this profile requires a custom image
// build. A profile needs a build if it declares packages to install or
// a build script to run.
func (p *Profile) NeedsImageBuild() bool {
	return len(p.Packages) > 0 || strings.TrimSpace(p.Build) != ""
}

// Dir returns the absolute path to ~/.config/pen/profiles.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, configDir, profilesDir), nil
}

// Path returns the absolute path to a profile's TOML file.
func Path(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+fileExt), nil
}

// Load reads and validates the profile identified by name. A missing
// file is reported by wrapping os.ErrNotExist so callers can use
// errors.Is to distinguish "no such profile" from parse errors.
func Load(name string) (*Profile, error) {
	path, err := Path(name)
	if err != nil {
		return nil, err
	}

	var p Profile
	md, err := toml.DecodeFile(path, &p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("profile %q not found at %s: %w", name, path, os.ErrNotExist)
		}
		return nil, fmt.Errorf("parsing profile %q: %w", name, err)
	}

	if undec := md.Undecoded(); len(undec) > 0 {
		keys := make([]string, 0, len(undec))
		for _, k := range undec {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("profile %q: unknown keys: %s", name, strings.Join(keys, ", "))
	}

	p.Name = name

	if err := validate(&p); err != nil {
		return nil, fmt.Errorf("profile %q: %w", name, err)
	}
	return &p, nil
}

// List returns every profile in Dir(), sorted by name. Profiles that
// fail to parse are skipped; the per-file errors are returned in the
// second slice so callers can surface them to the user. The third
// return value is non-nil only if the profile directory itself is
// unreadable (missing is fine — returns empty slices).
func List() ([]*Profile, []error, error) {
	dir, err := Dir()
	if err != nil {
		return nil, nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("reading profiles dir: %w", err)
	}

	var profiles []*Profile
	var perFileErrs []error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, fileExt) {
			continue
		}
		base := strings.TrimSuffix(name, fileExt)
		p, err := Load(base)
		if err != nil {
			perFileErrs = append(perFileErrs, err)
			continue
		}
		profiles = append(profiles, p)
	}
	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].Name < profiles[j].Name
	})
	return profiles, perFileErrs, nil
}

// validateName checks a profile name for filesystem safety. It rejects
// names that contain path separators, start with a dot or dash, or
// would otherwise not round-trip cleanly as <dir>/<name>.toml.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("profile name is empty")
	}
	if len(name) > maxNameLen {
		return fmt.Errorf("profile name %q is too long (max %d chars)", name, maxNameLen)
	}
	if !nameRE.MatchString(name) {
		return fmt.Errorf("invalid profile name %q (must match [A-Za-z0-9][A-Za-z0-9_-]*)", name)
	}
	return nil
}

func validate(p *Profile) error {
	for i, pkg := range p.Packages {
		if pkg == "" {
			return fmt.Errorf("packages[%d]: empty package name", i)
		}
		if !packageRE.MatchString(pkg) {
			return fmt.Errorf("packages[%d]: invalid package name %q", i, pkg)
		}
	}
	if p.Disk.Size != "" {
		if _, err := vm.ParseDiskSize(p.Disk.Size); err != nil {
			return fmt.Errorf("disk.size: %w", err)
		}
	}
	return nil
}
