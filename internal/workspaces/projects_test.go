package workspaces

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

func TestOrderedProjectReferencesAndRevisionGuards(t *testing.T) {
	ctx := context.Background()
	paths := storage.Paths{ConfigDir: filepath.Join(t.TempDir(), "config")}
	service := testService(t, paths)
	workspace := ok(t, service.Create(ctx, Draft{"Coding", ""})).Workspaces[0]
	codec, _ := storage.NewProjectCodec()
	registry, err := storage.OpenRepository(paths.ProjectsDir(), codec)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	choices := []ProjectSelection{}
	for _, name := range []string{"API", "Worker"} {
		id, _ := domain.NewID()
		now := time.Now().UTC()
		// An unavailable source directory does not prevent associating its registration.
		project := domain.Project{ID: id, Name: name, Path: "/offline/" + name, Source: domain.SourceImported, CreatedAt: now, UpdatedAt: now}
		if err := registry.Create(ctx, project); err != nil {
			t.Fatal(err)
		}
		snapshot, err := registry.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		choices = append(choices, ProjectSelection{id, snapshot.Revision})
	}
	stale := workspace
	workspace = ok(t, service.SetProjects(ctx, target(workspace), choices)).Workspaces[0]
	expected := []domain.ID{choices[0].ID, choices[1].ID}
	if !reflect.DeepEqual(workspace.ProjectIDs, expected) || workspace.ProjectCount != 2 {
		t.Fatal(workspace)
	}
	if got := service.SetProjects(ctx, target(stale), nil); got.Code != "conflict" || got.Committed {
		t.Fatal(got)
	}
	if got := service.SetProjects(ctx, target(workspace), []ProjectSelection{choices[0], choices[0]}); got.Code != "validation" {
		t.Fatal(got)
	}
	if err := registry.Update(ctx, choices[0].ID, choices[0].Revision, func(p domain.Project) (domain.Project, error) { p.Name = "Changed"; return p, nil }); err != nil {
		t.Fatal(err)
	}
	if got := service.SetProjects(ctx, target(workspace), choices); got.Code != "conflict" || got.Committed {
		t.Fatal(got)
	}
	latest, _ := registry.Get(ctx, choices[0].ID)
	choices[0].Revision = latest.Revision
	choices[0], choices[1] = choices[1], choices[0]
	workspace = ok(t, service.SetProjects(ctx, target(workspace), choices)).Workspaces[0]
	if !reflect.DeepEqual(workspace.ProjectIDs, []domain.ID{expected[1], expected[0]}) {
		t.Fatal(workspace)
	}
	copyState := ok(t, service.Duplicate(ctx, target(workspace), Draft{"Copy", ""}))
	if !reflect.DeepEqual(copyState.Workspaces[1].ProjectIDs, workspace.ProjectIDs) {
		t.Fatal(copyState)
	}
	// A legacy missing reference is retained until explicitly removed.
	if err := registry.Delete(ctx, choices[0].ID, choices[0].Revision); err != nil {
		t.Fatal(err)
	}
	choices[0].Revision = ""
	workspace = ok(t, service.SetProjects(ctx, target(workspace), choices)).Workspaces[0]
	workspace = ok(t, service.SetProjects(ctx, target(workspace), choices[1:])).Workspaces[0]
	if got := service.SetProjects(ctx, target(workspace), choices); got.Code != "project_unavailable" || got.Committed {
		t.Fatal(got)
	}
	// Invalid project bytes fail closed and do not rewrite the workspace.
	filename := filepath.Join(paths.ProjectsDir(), string(choices[1].ID)+".json")
	if err := os.WriteFile(filename, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := service.SetProjects(ctx, target(workspace), choices[1:]); got.Code != "project_storage" || got.Committed {
		t.Fatal(got)
	}
	unchanged := ok(t, service.List(ctx)).Workspaces[0]
	if unchanged.Revision != workspace.Revision {
		t.Fatal("failed selection changed workspace")
	}
	// Explicitly detaching references does not need to decode those registrations.
	workspace = ok(t, service.SetProjects(ctx, target(workspace), nil)).Workspaces[0]
	if workspace.ProjectCount != 0 {
		t.Fatal(workspace)
	}
}
