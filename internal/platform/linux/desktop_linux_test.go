//go:build linux && cgo

package linux

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"workspace-manager/internal/applications"
)

type lookupResult struct {
	Entry       applications.DesktopEntry
	Unavailable bool
	Invalid     bool
}

type discoveryResult struct {
	Entries     []applications.DesktopEntry
	Lookups     map[string]lookupResult
	LaunchError string
}

// GIO caches environment and desktop metadata process-wide. Fixture runs start
// a fresh test process with isolated XDG roots, never mutate an active GLib
// environment and never call the user's installed desktop applications.
func TestDesktopCatalogHelper(t *testing.T) {
	if os.Getenv("WORKSPACE_DESKTOP_FIXTURE_CHILD") != "1" {
		t.Skip("fixture subprocess only")
	}
	catalog := DesktopCatalog{}
	entries, err := catalog.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result := discoveryResult{Entries: entries, Lookups: map[string]lookupResult{}}
	var ids []string
	if err := json.Unmarshal([]byte(os.Getenv("WORKSPACE_DESKTOP_FIXTURE_IDS")), &ids); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		entry, err := catalog.Lookup(context.Background(), id)
		if err != nil && !errors.Is(err, applications.ErrUnavailable) && !errors.Is(err, applications.ErrInvalidDesktopID) {
			t.Fatal(err)
		}
		result.Lookups[id] = lookupResult{entry, errors.Is(err, applications.ErrUnavailable), errors.Is(err, applications.ErrInvalidDesktopID)}
	}
	if mode := os.Getenv("WORKSPACE_DESKTOP_LAUNCH_MODE"); mode != "" {
		spec, err := catalog.Review(context.Background(), ids[0])
		if err == nil {
			ctx := context.Background()
			switch mode {
			case "changed":
				file, openErr := os.OpenFile(spec.Entry.DesktopFile, os.O_APPEND|os.O_WRONLY, 0)
				if openErr != nil {
					t.Fatal(openErr)
				}
				_, writeErr := file.WriteString("\nX-Review-Changed=true\n")
				file.Close()
				if writeErr != nil {
					t.Fatal(writeErr)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err = catalog.Launch(ctx, spec)
		}
		switch {
		case err == nil:
			result.LaunchError = "requested"
		case errors.Is(err, applications.ErrLaunchChanged):
			result.LaunchError = "changed"
		case errors.Is(err, applications.ErrUnavailable):
			result.LaunchError = "unavailable"
		case errors.Is(err, context.Canceled):
			result.LaunchError = "cancelled"
		default:
			result.LaunchError = "failed"
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

type desktopFixture struct{ root, user, first, second, launchMode string }

func newDesktopFixture(t *testing.T) desktopFixture {
	t.Helper()
	root := t.TempDir()
	fixture := desktopFixture{root: root, user: filepath.Join(root, "user"), first: filepath.Join(root, "system-first"), second: filepath.Join(root, "system-second")}
	for _, data := range []string{fixture.user, fixture.first, fixture.second} {
		if err := os.MkdirAll(filepath.Join(data, "applications"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func desktopFile(t *testing.T, dataRoot, name, contents string) string {
	t.Helper()
	path := filepath.Join(dataRoot, "applications", filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func applicationFile(name, extra string) string {
	return "[Desktop Entry]\nType=Application\nName=" + name + "\nExec=/bin/true\n" + extra
}

func fixtureCatalogue(t *testing.T, fixture desktopFixture, desktop, locale string, ids ...string) discoveryResult {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestDesktopCatalogHelper$")
	// Keep HOME untouched. All relevant XDG paths are explicit test-owned roots.
	for _, variable := range os.Environ() {
		key, _, _ := strings.Cut(variable, "=")
		if strings.HasPrefix(key, "XDG_") || strings.HasPrefix(key, "WORKSPACE_DESKTOP_") || slices.Contains([]string{"PATH", "LANG", "LANGUAGE", "LC_ALL", "LC_MESSAGES"}, key) {
			continue
		}
		command.Env = append(command.Env, variable)
	}
	encodedIDs, err := json.Marshal(ids)
	if err != nil {
		t.Fatal(err)
	}
	command.Env = append(command.Env,
		"WORKSPACE_DESKTOP_LAUNCH_MODE="+fixture.launchMode,
		"WORKSPACE_DESKTOP_FIXTURE_CHILD=1", "WORKSPACE_DESKTOP_FIXTURE_IDS="+string(encodedIDs),
		"XDG_DATA_HOME="+fixture.user, "XDG_DATA_DIRS="+fixture.first+":"+fixture.second,
		"XDG_CONFIG_HOME="+filepath.Join(fixture.root, "config"), "XDG_CONFIG_DIRS="+filepath.Join(fixture.root, "config-system"),
		"XDG_CACHE_HOME="+filepath.Join(fixture.root, "cache"), "XDG_STATE_HOME="+filepath.Join(fixture.root, "state"),
		"XDG_RUNTIME_DIR="+filepath.Join(fixture.root, "runtime"), "XDG_CURRENT_DESKTOP="+desktop,
		"PATH="+filepath.Join(fixture.root, "bin")+":/usr/bin:/bin",
		"LANG="+locale, "LC_ALL="+locale, "LANGUAGE=", "LC_MESSAGES="+locale,
	)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("fixture discovery: %v\n%s\n%s", err, output, stderr.String())
	}
	var result discoveryResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("invalid fixture result: %v\n%s", err, output)
	}
	return result
}

func byID(entries []applications.DesktopEntry) map[string]applications.DesktopEntry {
	result := make(map[string]applications.DesktopEntry, len(entries))
	for _, entry := range entries {
		result[entry.ID] = entry
	}
	return result
}

func TestDesktopDiscoveryPrecedenceAndIdentity(t *testing.T) {
	fixture := newDesktopFixture(t)
	desktopFile(t, fixture.second, "org.example.Shared.desktop", applicationFile("Lowest priority", ""))
	desktopFile(t, fixture.first, "org.example.Shared.desktop", applicationFile("System priority", ""))
	user := desktopFile(t, fixture.user, "org.example.Shared.desktop", applicationFile("User priority", ""))
	desktopFile(t, fixture.second, "org.example.System.desktop", applicationFile("System second", ""))
	first := desktopFile(t, fixture.first, "org.example.System.desktop", applicationFile("System first", ""))
	desktopFile(t, fixture.first, "org.example.Hidden.desktop", applicationFile("Masked system app", ""))
	// A user tombstone does not need a name or command to mask the system ID.
	desktopFile(t, fixture.user, "org.example.Hidden.desktop", "[Desktop Entry]\nHidden=true\n")
	nested := desktopFile(t, fixture.first, "vendor/Editor.desktop", applicationFile("Nested editor", ""))
	desktopFile(t, fixture.user, "org.example.EqualA.desktop", applicationFile("Same name", ""))
	desktopFile(t, fixture.user, "org.example.EqualB.desktop", applicationFile("Same name", ""))
	result := fixtureCatalogue(t, fixture, "ubuntu:GNOME", "C.UTF-8", "org.example.Hidden.desktop", "vendor-Editor.desktop", "org.example.Missing.desktop", "../../outside.desktop")
	entries := byID(result.Entries)
	if len(entries) != 5 {
		t.Fatalf("unexpected IDs: %+v", entries)
	}
	if entries["org.example.Shared.desktop"].DesktopFile != user || entries["org.example.Shared.desktop"].Name != "User priority" {
		t.Fatal("user override lost")
	}
	if entries["org.example.System.desktop"].DesktopFile != first {
		t.Fatal("system order lost")
	}
	if entries["vendor-Editor.desktop"].DesktopFile != nested || result.Lookups["vendor-Editor.desktop"].Entry.ID != "vendor-Editor.desktop" {
		t.Fatal("nested ID mapping lost")
	}
	if !result.Lookups["org.example.Hidden.desktop"].Unavailable || !result.Lookups["org.example.Missing.desktop"].Unavailable || !result.Lookups["../../outside.desktop"].Invalid {
		t.Fatal(result.Lookups)
	}
	// Equal display names remain independently addressable and deterministically sorted.
	var equal []string
	for _, entry := range result.Entries {
		if entry.Name == "Same name" {
			equal = append(equal, entry.ID)
		}
	}
	if !slices.Equal(equal, []string{"org.example.EqualA.desktop", "org.example.EqualB.desktop"}) {
		t.Fatal(equal)
	}
}

func TestDesktopDiscoveryMenuVisibility(t *testing.T) {
	fixture := newDesktopFixture(t)
	for id, extra := range map[string]string{
		"Visible": "", "NoDisplay": "NoDisplay=true\n", "OnlyGNOME": "OnlyShowIn=GNOME;\n",
		"OnlyKDE": "OnlyShowIn=KDE;\n", "NotGNOME": "NotShowIn=GNOME;\n", "NotKDE": "NotShowIn=KDE;\n",
	} {
		desktopFile(t, fixture.user, "org.example."+id+".desktop", applicationFile(id, extra))
	}
	result := fixtureCatalogue(t, fixture, "ubuntu:GNOME", "C.UTF-8", "org.example.NoDisplay.desktop", "org.example.OnlyKDE.desktop")
	entries := byID(result.Entries)
	for _, id := range []string{"Visible", "OnlyGNOME", "NotKDE"} {
		if _, ok := entries["org.example."+id+".desktop"]; !ok {
			t.Fatalf("missing %s", id)
		}
	}
	if len(entries) != 3 {
		t.Fatal(entries)
	}
	for _, lookup := range result.Lookups {
		if lookup.Unavailable || lookup.Entry.Visible || lookup.Entry.ID == "" {
			t.Fatal(lookup)
		}
	}
	// The spec uses the first matching current desktop, not an unordered set.
	desktopFile(t, fixture.user, "org.example.Ordered.desktop", applicationFile("Ordered", "OnlyShowIn=KDE;\nNotShowIn=GNOME;\n"))
	for _, test := range []struct {
		desktop string
		visible bool
	}{{"GNOME:KDE", false}, {"KDE:GNOME", true}} {
		result := fixtureCatalogue(t, fixture, test.desktop, "C.UTF-8", "org.example.Ordered.desktop")
		if result.Lookups["org.example.Ordered.desktop"].Entry.Visible != test.visible {
			t.Fatalf("desktop ordering %s: %+v", test.desktop, result)
		}
	}
}

func TestDesktopDiscoveryLocaleAndMetadata(t *testing.T) {
	fixture := newDesktopFixture(t)
	contents, err := os.ReadFile("testdata/org.example.Editor.desktop")
	if err != nil {
		t.Fatal(err)
	}
	path := desktopFile(t, fixture.user, "org.example.Editor.desktop", string(contents))
	for _, test := range []struct{ locale, name string }{
		{"sr_RS.UTF-8@latin", "Editor region modifier"}, {"sr_RS.UTF-8", "Editor region"},
		{"sr_ME.UTF-8@latin", "Editor modifier"}, {"sr_ME.UTF-8", "Editor language"}, {"C.UTF-8", "Example Editor"},
	} {
		result := fixtureCatalogue(t, fixture, "GNOME", test.locale)
		if len(result.Entries) != 1 {
			t.Fatal(result)
		}
		entry := result.Entries[0]
		if entry.Name != test.name || entry.DesktopFile != path || entry.GenericName != "Text Editor" || entry.Description != "Edit files\nWith a second line" || entry.Icon != "accessories-text-editor" || !entry.Terminal || entry.DBusActivatable {
			t.Fatalf("locale %s metadata: %+v", test.locale, entry)
		}
		if entry.Commandline != `/bin/true "two words" %U %i %c %k %%` || entry.Executable != "/bin/true" {
			t.Fatalf("launch metadata changed: %+v", entry)
		}
	}
}

func TestDesktopDiscoveryAvailabilityAndReadOnlyBehavior(t *testing.T) {
	fixture := newDesktopFixture(t)
	missing := filepath.Join(fixture.root, "not-installed")
	desktopFile(t, fixture.user, "org.example.MissingTryExec.desktop", applicationFile("Missing TryExec", "TryExec="+missing+"\n"))
	desktopFile(t, fixture.user, "org.example.MissingExec.desktop", "[Desktop Entry]\nType=Application\nName=Missing Exec\nExec="+missing+"\n")
	desktopFile(t, fixture.user, "org.example.Malformed.desktop", "not a desktop file\n")
	desktopFile(t, fixture.first, "org.example.Malformed.desktop", applicationFile("Must not fall back", ""))
	desktopFile(t, fixture.user, "org.example.Link.desktop", "[Desktop Entry]\nType=Link\nName=Link\nURL=https://example.org\n")
	desktopFile(t, fixture.user, "org.example.DBusOnly.desktop", "[Desktop Entry]\nType=Application\nName=D-Bus application\nDBusActivatable=true\n")
	sentinel := filepath.Join(fixture.root, "must-not-exist")
	program := filepath.Join(fixture.root, "must-not-run")
	if err := os.WriteFile(program, []byte("#!/bin/sh\ntouch '"+sentinel+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := desktopFile(t, fixture.user, "org.example.ReadOnly.desktop", "[Desktop Entry]\nType=Application\nName=Read only\nTryExec="+program+"\nExec="+program+"\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result := fixtureCatalogue(t, fixture, "GNOME", "C.UTF-8", "org.example.MissingTryExec.desktop", "org.example.MissingExec.desktop", "org.example.Malformed.desktop")
	entries := byID(result.Entries)
	if len(entries) != 2 || !entries["org.example.DBusOnly.desktop"].DBusActivatable || entries["org.example.DBusOnly.desktop"].Executable != "" || entries["org.example.ReadOnly.desktop"].ID == "" {
		t.Fatal(entries)
	}
	for _, lookup := range result.Lookups {
		if !lookup.Unavailable {
			t.Fatal(lookup)
		}
	}
	if _, err := os.Stat(sentinel); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("discovery executed an application", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("discovery rewrote desktop metadata", err)
	}
}

func TestDesktopDiscoveryPathSearchAndSymlinkedEntries(t *testing.T) {
	fixture := newDesktopFixture(t)
	bin := filepath.Join(fixture.root, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "fixture-editor"), []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	desktopFile(t, fixture.user, "org.example.SearchPath.desktop", "[Desktop Entry]\nType=Application\nName=PATH editor\nTryExec=fixture-editor\nExec=fixture-editor --literal %F\n")
	if err := os.WriteFile(filepath.Join(bin, "non-executable"), []byte("not executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	desktopFile(t, fixture.user, "org.example.NotExecutable.desktop", applicationFile("Not executable", "TryExec=non-executable\n"))
	target := filepath.Join(fixture.root, "linked.desktop")
	if err := os.WriteFile(target, []byte(applicationFile("Linked registration", "")), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(fixture.user, "applications", "org.example.Linked.desktop")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	result := fixtureCatalogue(t, fixture, "GNOME", "C.UTF-8", "org.example.Linked.desktop", "org.example.NotExecutable.desktop")
	entries := byID(result.Entries)
	if len(entries) != 2 || entries["org.example.SearchPath.desktop"].Executable != "fixture-editor" || entries["org.example.Linked.desktop"].ID == "" || result.Lookups["org.example.Linked.desktop"].Entry.ID == "" || !result.Lookups["org.example.NotExecutable.desktop"].Unavailable {
		t.Fatal(result)
	}
}

func TestDesktopDiscoveryEmptyAndCancelled(t *testing.T) {
	fixture := newDesktopFixture(t)
	result := fixtureCatalogue(t, fixture, "GNOME", "C.UTF-8")
	if result.Entries == nil || len(result.Entries) != 0 {
		t.Fatal("empty catalogue must be a successful empty list", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	catalog := DesktopCatalog{}
	if entries, err := catalog.Discover(ctx); entries != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(entries, err)
	}
	if entry, err := catalog.Lookup(ctx, "org.example.App.desktop"); entry.ID != "" || !errors.Is(err, context.Canceled) {
		t.Fatal(entry, err)
	}
}

func TestInstalledDesktopCatalogue(t *testing.T) {
	if os.Getenv("WORKSPACE_MANAGER_DESKTOP_SMOKE") != "1" {
		t.Skip("opt-in read-only host check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	catalog := DesktopCatalog{}
	entries, err := catalog.Discover(ctx)
	if err != nil || len(entries) == 0 {
		t.Fatalf("host discovery: %d entries, %v", len(entries), err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if !entry.Visible || seen[entry.ID] {
			t.Fatal("duplicate ID or invisible entry")
		}
		seen[entry.ID] = true
		resolved, err := catalog.Lookup(ctx, entry.ID)
		if err != nil || resolved.ID != entry.ID || resolved.DesktopFile != entry.DesktopFile {
			t.Fatal("host ID did not resolve consistently", err)
		}
	}
	t.Logf("discovered and resolved %d visible desktop applications without launching them", len(entries))
}
