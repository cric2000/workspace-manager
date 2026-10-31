package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestXDGResolution(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want Paths
	}{
		{
			name: "home defaults",
			env:  map[string]string{"HOME": "/home/developer"},
			want: Paths{"/home/developer/.config/workspace-manager", "/home/developer/.local/state/workspace-manager", "/home/developer/.cache/workspace-manager"},
		},
		{
			name: "absolute overrides without HOME",
			env:  map[string]string{"XDG_CONFIG_HOME": "/custom config", "XDG_STATE_HOME": "/custom state", "XDG_CACHE_HOME": "/custom cache"},
			want: Paths{"/custom config/workspace-manager", "/custom state/workspace-manager", "/custom cache/workspace-manager"},
		},
		{
			name: "relative values ignored",
			env:  map[string]string{"HOME": "/home/developer", "XDG_CONFIG_HOME": "relative", "XDG_STATE_HOME": "~/state", "XDG_CACHE_HOME": "../cache"},
			want: Paths{"/home/developer/.config/workspace-manager", "/home/developer/.local/state/workspace-manager", "/home/developer/.cache/workspace-manager"},
		},
		{
			name: "independent overrides and clean paths",
			env:  map[string]string{"HOME": "/home/developer", "XDG_CONFIG_HOME": "/custom/../config/"},
			want: Paths{"/config/workspace-manager", "/home/developer/.local/state/workspace-manager", "/home/developer/.cache/workspace-manager"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths, err := pathsFromEnvironment(func(key string) string { return tc.env[key] })
			if err != nil {
				t.Fatal(err)
			}
			if paths != tc.want {
				t.Fatalf("paths = %+v, want %+v", paths, tc.want)
			}
		})
	}
}

func TestXDGRejectsUnavailableDefaultsAndInvalidPaths(t *testing.T) {
	for _, env := range []map[string]string{
		{}, {"HOME": "relative"}, {"HOME": "/home/invalid\x00"},
		{"XDG_CONFIG_HOME": "/config"}, // State and cache still need a HOME.
		{"HOME": "/home/dev", "XDG_STATE_HOME": "/invalid\x00"},
	} {
		paths, err := pathsFromEnvironment(func(key string) string { return env[key] })
		if err == nil || paths != (Paths{}) {
			t.Fatalf("expected an error and no partially resolved paths, got %+v / %v", paths, err)
		}
	}
}

func TestResolvePathsReadsCurrentEnvironmentWithoutCreatingDirectories(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	paths, err := ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{
		paths.SettingsFile():  filepath.Join(root, ".config/workspace-manager/settings.json"),
		paths.WorkspacesDir(): filepath.Join(root, ".config/workspace-manager/profiles"),
		paths.ProjectsDir():   filepath.Join(root, ".config/workspace-manager/projects"),
		paths.SessionsDir():   filepath.Join(root, ".local/state/workspace-manager/sessions"),
	}
	for actual, want := range expected {
		if actual != want {
			t.Errorf("path = %q, want %q", actual, want)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("resolution must not create files or directories: %v / %v", entries, err)
	}
	// Resolve on every call; don't cache XDG settings as package globals.
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "changed"))
	updated, err := ResolvePaths()
	if err != nil || updated.StateDir != filepath.Join(root, "changed/workspace-manager") {
		t.Fatalf("did not read changed environment: %+v / %v", updated, err)
	}
}
