// Package envject handles secure injection of environment variables from host
// to guest via the shared directory.
//
// The host writes a dotfile (.pen-env) to the shared directory before boot.
// The guest init reads it into tmpfs (/run/pen-env) and deletes the original.
package envject

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// EnvFileName is the name of the env file written to the shared directory.
const EnvFileName = ".pen-env"

// envNameRE matches valid POSIX shell variable names.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateName returns an error if name is not a valid shell variable name.
// A valid name starts with a letter or underscore and contains only letters,
// digits, and underscores. This catches typos like "FOO-BAR" or "1FOO" before
// they produce a broken env file in the guest.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("env var name is empty")
	}
	if !envNameRE.MatchString(name) {
		return fmt.Errorf("invalid env var name %q (must match [A-Za-z_][A-Za-z0-9_]*)", name)
	}
	return nil
}

// EnvSpec describes environment variables to inject.
type EnvSpec struct {
	// FromHost are key names whose values are read from the host environment.
	FromHost []string
	// Explicit are KEY=VALUE pairs provided directly.
	Explicit map[string]string
}

// Resolve resolves the EnvSpec into a flat map of KEY=VALUE pairs.
func (s *EnvSpec) Resolve() map[string]string {
	env := make(map[string]string, len(s.FromHost)+len(s.Explicit))
	for _, key := range s.FromHost {
		if val, ok := os.LookupEnv(key); ok {
			env[key] = val
		}
	}
	for k, v := range s.Explicit {
		env[k] = v
	}
	return env
}

// IsEmpty reports whether the spec has any env vars to inject.
func (s *EnvSpec) IsEmpty() bool {
	return len(s.FromHost) == 0 && len(s.Explicit) == 0
}

// WriteEnvFile writes the resolved environment variables to a dotfile in the
// shared directory. The guest init will read and delete it.
func WriteEnvFile(shareDir string, spec *EnvSpec) error {
	env := spec.Resolve()
	if len(env) == 0 {
		return nil
	}

	var b strings.Builder
	for k, v := range env {
		if err := ValidateName(k); err != nil {
			return err
		}
		// Shell-safe: single-quote the value, escaping embedded single quotes.
		fmt.Fprintf(&b, "export %s='%s'\n", k, strings.ReplaceAll(v, "'", "'\\''"))
	}

	// Open with O_EXCL|O_NOFOLLOW so a precreated .pen-env symlink in an
	// untrusted workspace cannot redirect this write to a sensitive host
	// file (e.g., a symlink → ~/.zshrc would otherwise have its target
	// clobbered with the injected secrets). If the file already exists,
	// we refuse rather than overwrite.
	path := filepath.Join(shareDir, EnvFileName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return fmt.Errorf("creating env file: %w", err)
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		os.Remove(path)
		return fmt.Errorf("writing env file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return fmt.Errorf("closing env file: %w", err)
	}

	return nil
}

// CleanupEnvFile removes the env dotfile from the shared directory.
func CleanupEnvFile(shareDir string) {
	os.Remove(filepath.Join(shareDir, EnvFileName))
}
