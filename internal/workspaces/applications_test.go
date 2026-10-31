package workspaces

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"workspace-manager/internal/applications"
	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

type applicationFixture struct {
	entries map[string]applications.DesktopEntry
	calls   int
}

func (catalog *applicationFixture) Lookup(_ context.Context, id string) (applications.DesktopEntry, error) {
	catalog.calls++
	if entry, ok := catalog.entries[id]; ok {
		return entry, nil
	}
	return applications.DesktopEntry{}, applications.ErrUnavailable
}

func TestWorkspaceApplicationEditingPreservesMetadataAndMissingReferences(t *testing.T) {
	ctx := context.Background()
	paths := storage.Paths{ConfigDir: filepath.Join(t.TempDir(), "config")}
	service := testService(t, paths)
	const editor, browser = "org.example.Editor.desktop", "org.example.Browser.desktop"
	catalog := &applicationFixture{entries: map[string]applications.DesktopEntry{editor: {ID: editor, Visible: true}, browser: {ID: browser, Visible: true}}}
	service.catalog = catalog
	original := ok(t, service.Create(ctx, Draft{"Coding", "Original description"})).Workspaces[0]
	choices := []domain.WorkspaceApplication{{DesktopID: editor, DuplicatePolicy: domain.SkipIfRunning}, {DesktopID: browser, DuplicatePolicy: domain.AlwaysLaunch}}
	saved := ok(t, service.SetApplications(ctx, target(original), choices)).Workspaces[0]
	if !reflect.DeepEqual(saved.Applications, choices) || saved.Name != original.Name || saved.Description != original.Description || !saved.CreatedAt.Equal(original.CreatedAt) {
		t.Fatal(saved)
	}
	choices[0].DuplicatePolicy = domain.AlwaysLaunch
	if saved.Applications[0].DuplicatePolicy != domain.SkipIfRunning {
		t.Fatal("draft shares mutable memory with saved view")
	}
	calls := catalog.calls
	if result := service.SetApplications(ctx, target(original), choices); result.Code != "conflict" || catalog.calls != calls {
		t.Fatal("stale mutation reached discovery", result)
	}
	delete(catalog.entries, editor)
	retained := []domain.WorkspaceApplication{{DesktopID: browser, DuplicatePolicy: domain.SkipIfRunning}, {DesktopID: editor, DuplicatePolicy: domain.AlwaysLaunch}}
	saved = ok(t, service.SetApplications(ctx, target(saved), retained)).Workspaces[0]
	if !reflect.DeepEqual(saved.Applications, retained) {
		t.Fatal("missing application or order was lost")
	}
	saved = ok(t, service.Edit(ctx, target(saved), Draft{"Backend", "Renamed"})).Workspaces[0]
	if !reflect.DeepEqual(saved.Applications, retained) {
		t.Fatal("name edit erased applications")
	}
	state := ok(t, service.Duplicate(ctx, target(saved), Draft{"Copy", "Copied"}))
	if len(state.Workspaces) != 2 || !reflect.DeepEqual(state.Workspaces[1].Applications, retained) {
		t.Fatal("duplication lost applications", state)
	}
	copy := state.Workspaces[1]
	copy.Applications[0].DuplicatePolicy = domain.AlwaysLaunch
	if state.Workspaces[0].Applications[0].DuplicatePolicy != domain.SkipIfRunning {
		t.Fatal("duplicate shares application slice")
	}
	if err := service.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	reopened := testService(t, paths)
	state = ok(t, reopened.List(ctx))
	if !reflect.DeepEqual(state.Workspaces[0].Applications, retained) || !reflect.DeepEqual(state.Workspaces[1].Applications, retained) {
		t.Fatal("application configuration did not persist", state)
	}
	cleared := ok(t, reopened.SetApplications(ctx, target(state.Workspaces[0]), []domain.WorkspaceApplication{}))
	if len(cleared.Workspaces[0].Applications) != 0 || !reflect.DeepEqual(cleared.Workspaces[1].Applications, retained) {
		t.Fatal("clear changed another profile")
	}
}

func TestWorkspaceApplicationSelectionRejectsInvalidAndUnavailableAdditions(t *testing.T) {
	ctx := context.Background()
	service := testService(t, storage.Paths{ConfigDir: filepath.Join(t.TempDir(), "config")})
	service.catalog = &applicationFixture{entries: map[string]applications.DesktopEntry{"org.example.Hidden.desktop": {ID: "org.example.Hidden.desktop", Visible: false}}}
	original := ok(t, service.Create(ctx, Draft{"Coding", ""})).Workspaces[0]
	for _, selection := range [][]domain.WorkspaceApplication{
		{{DesktopID: "../../arbitrary.desktop", DuplicatePolicy: domain.SkipIfRunning}},
		{{DesktopID: "org.example.Editor.desktop", DuplicatePolicy: "kill_existing"}},
		{{DesktopID: "org.example.Editor.desktop", DuplicatePolicy: domain.SkipIfRunning}, {DesktopID: "org.example.Editor.desktop", DuplicatePolicy: domain.AlwaysLaunch}},
	} {
		if result := service.SetApplications(ctx, target(original), selection); result.Code != "validation" || result.Committed {
			t.Fatal(result)
		}
	}
	for _, id := range []domain.DesktopID{"org.example.Missing.desktop", "org.example.Hidden.desktop"} {
		if result := service.SetApplications(ctx, target(original), []domain.WorkspaceApplication{{DesktopID: id, DuplicatePolicy: domain.SkipIfRunning}}); result.Code != "application_unavailable" || result.Committed {
			t.Fatal(result)
		}
	}
	current := ok(t, service.List(ctx)).Workspaces[0]
	if current.Revision != original.Revision || len(current.Applications) != 0 {
		t.Fatal("failed selection changed the profile")
	}
}
