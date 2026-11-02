// Package storage contains local persistence infrastructure. Path resolution is
// separate from directory creation, permissions and repository I/O.
package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const applicationDirectory = "workspace-manager"

// Paths contains application-specific directories, not the shared XDG roots.
// Resolution is lexical: it neither creates directories nor establishes that a
// path is safe to open. Repositories must check filesystem boundaries at I/O time.
type Paths struct {
	ConfigDir string
	StateDir  string
	CacheDir  string
}

func ResolvePaths() (Paths, error) {
	return pathsFromEnvironment(os.Getenv)
}

func pathsFromEnvironment(getenv func(string) string) (Paths, error) {
	resolve := func(variable, fallback string) (string, error) {
		base := getenv(variable)
		if !filepath.IsAbs(base) {
			// Empty and relative XDG values use the specified home fallback.
			home := getenv("HOME")
			if !filepath.IsAbs(home) || strings.ContainsRune(home, 0) {
				return "", errors.New("an absolute HOME is required for XDG defaults")
			}
			base = filepath.Join(home, fallback)
		}
		if strings.ContainsRune(base, 0) {
			return "", fmt.Errorf("%s must not contain NUL", variable)
		}
		return filepath.Join(base, applicationDirectory), nil
	}
	config, err := resolve("XDG_CONFIG_HOME", ".config")
	if err != nil {
		return Paths{}, fmt.Errorf("config directory: %w", err)
	}
	state, err := resolve("XDG_STATE_HOME", ".local/state")
	if err != nil {
		return Paths{}, fmt.Errorf("state directory: %w", err)
	}
	cache, err := resolve("XDG_CACHE_HOME", ".cache")
	if err != nil {
		return Paths{}, fmt.Errorf("cache directory: %w", err)
	}
	return Paths{ConfigDir: config, StateDir: state, CacheDir: cache}, nil
}

func (paths Paths) SettingsFile() string {
	return filepath.Join(paths.ConfigDir, "settings.json")
}

func (paths Paths) WorkspacesDir() string {
	return filepath.Join(paths.ConfigDir, "profiles")
}

// Workspace edits, project association and guarded project removal all take
// this lock before repository locks (and before the project-operation lock).
func (paths Paths) WorkspaceOperationsDir() string {
	return filepath.Join(paths.ConfigDir, "workspace-operations")
}

func (paths Paths) ProjectsDir() string {
	return filepath.Join(paths.ConfigDir, "projects")
}

func (paths Paths) SessionsDir() string {
	return filepath.Join(paths.StateDir, "sessions")
}

func (paths Paths) SetupLogsDir() string {
	return filepath.Join(paths.StateDir, "setup-logs")
}

func (paths Paths) CreationLogsDir() string { return filepath.Join(paths.StateDir, "creation-history") }

func (paths Paths) BootstrapRecoveryDir() string {
	return filepath.Join(paths.StateDir, "bootstrap-recovery")
}
