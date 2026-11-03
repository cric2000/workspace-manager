package projects

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
	"workspace-manager/internal/workspaces"
)

func TestRemovalGuardsReferencesAndPreservesSourceAndHistory(t *testing.T) {
	service, draft, paths := registryFixture(t)
	ctx := context.Background()
	view := requireSave(t, service, requireReview(t, service, draft)).State.Projects[0]
	target := ProjectTarget{view.Project.ID, view.Revision}
	source := filepath.Join(draft.Path, "source.txt")
	if err := os.WriteFile(source, []byte("keep exactly"), 0600); err != nil {
		t.Fatal(err)
	}
	// A saved session owns an independent project value, not a registry reference.
	sessionCodec, _ := storage.NewSessionCodec()
	sessions, err := storage.OpenRepository(filepath.Join(t.TempDir(), "sessions"), sessionCodec)
	if err != nil {
		t.Fatal(err)
	}
	defer sessions.Close()
	sessionID, _ := domain.NewID()
	if err := sessions.Create(ctx, domain.Session{ID: sessionID, Name: "Saved", CapturedAt: time.Now().UTC(), Projects: []domain.Project{view.Project}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := sessions.Get(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(paths.ProjectsDir(), string(target.ID)+".json")
	before, _ := os.ReadFile(filename)
	codec, _ := storage.NewWorkspaceCodec()
	profiles, err := storage.OpenRepository(paths.WorkspacesDir(), codec)
	if err != nil {
		t.Fatal(err)
	}
	defer profiles.Close()
	id, _ := domain.NewID()
	now := time.Now().UTC()
	workspace := domain.Workspace{ID: id, Name: "Coding", ProjectIDs: []domain.ID{target.ID}, CreatedAt: now, UpdatedAt: now}
	if err := profiles.Create(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	if got := service.RemoveProject(ctx, target); got.Code != "project_in_use" || got.Committed {
		t.Fatal(got)
	}
	saved, _ := profiles.Get(ctx, id)
	if err := profiles.Update(ctx, id, saved.Revision, func(w domain.Workspace) (domain.Workspace, error) { w.ProjectIDs = nil; return w, nil }); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(paths.WorkspacesDir(), string(id)+".json")
	profileBytes, _ := os.ReadFile(profilePath)
	if err := os.WriteFile(profilePath, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := service.RemoveProject(ctx, target); got.Code != "references_unavailable" || got.Committed {
		t.Fatal(got)
	}
	if after, _ := os.ReadFile(filename); !bytes.Equal(before, after) {
		t.Fatal("blocked removal rewrote registration")
	}
	future := bytes.Replace(profileBytes, []byte(`"schemaVersion": 4`), []byte(`"schemaVersion": 999`), 1)
	if bytes.Equal(profileBytes, future) {
		t.Fatal("fixture schema was not changed")
	}
	if err := os.WriteFile(profilePath, future, 0600); err != nil {
		t.Fatal(err)
	}
	if got := service.RemoveProject(ctx, target); got.Code != "references_unavailable" || got.Committed {
		t.Fatal(got)
	}
	if err := os.WriteFile(profilePath, profileBytes, 0600); err != nil {
		t.Fatal(err)
	}
	stale := target
	stale.Revision = storage.Revision("f" + string(target.Revision)[1:])
	if stale.Revision == target.Revision {
		stale.Revision = storage.Revision("e" + string(target.Revision)[1:])
	}
	if got := service.RemoveProject(ctx, stale); got.Code != "conflict" || got.Committed {
		t.Fatal(got)
	}
	if got := service.RemoveProject(ctx, target); got.Code != "" || !got.Committed || len(got.State.Projects) != 0 {
		t.Fatal(got)
	}
	if _, err := os.Stat(filename); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	afterSession, err := sessions.Get(ctx, sessionID)
	if err != nil || afterSession.Revision != snapshot.Revision || afterSession.Value.Projects[0].ID != target.ID {
		t.Fatal("session changed", afterSession, err)
	}
	history, err := service.registry.GetRevision(ctx, target.ID, target.Revision)
	if err != nil || history.Value.ID != target.ID {
		t.Fatal(history, err)
	}
	archive, err := os.ReadFile(filepath.Join(paths.ProjectsDir(), "history", string(target.ID), string(target.Revision)+".json"))
	if err != nil || !bytes.Equal(archive, before) {
		t.Fatal("exact approval history lost", err)
	}
	if after, _ := os.ReadFile(source); string(after) != "keep exactly" {
		t.Fatal("source touched")
	}
	if _, err := os.Stat(filepath.Join(draft.Path, "must-not-run")); !os.IsNotExist(err) {
		t.Fatal("command ran", err)
	}
}

func TestAssociationAndRemovalAcrossServiceHandlesCannotLeaveDanglingReference(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	registry := NewService(nil)
	defer registry.ServiceShutdown()
	profiles := workspaces.NewService(nil)
	defer profiles.ServiceShutdown()
	for i := 0; i < 12; i++ {
		view := requireSave(t, registry, requireReview(t, registry, RegistrationDraft{Name: "Project", Path: source})).State.Projects[0]
		name := fmt.Sprintf("Coding %02d", i)
		created := profiles.Create(ctx, workspaces.Draft{Name: name})
		if created.Code != "" {
			t.Fatal(created)
		}
		workspace := created.State.Workspaces[len(created.State.Workspaces)-1]
		// Find by identity in the sorted response, independent of sort position.
		for _, candidate := range created.State.Workspaces {
			if candidate.Name == name {
				workspace = candidate
			}
		}
		var attach workspaces.Result
		var remove RegistryResult
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			attach = profiles.SetProjects(ctx, workspaces.Target{ID: workspace.ID, Revision: workspace.Revision}, []workspaces.ProjectSelection{{ID: view.Project.ID, Revision: view.Revision}})
		}()
		go func() {
			defer wg.Done()
			remove = registry.RemoveProject(ctx, ProjectTarget{view.Project.ID, view.Revision})
		}()
		wg.Wait()
		if attach.Code == "" {
			if !attach.Committed || remove.Code != "project_in_use" || remove.Committed {
				t.Fatal(attach, remove)
			}
			fresh := profiles.List(ctx)
			for _, w := range fresh.State.Workspaces {
				if w.ID == workspace.ID {
					detached := profiles.SetProjects(ctx, workspaces.Target{ID: w.ID, Revision: w.Revision}, nil)
					if detached.Code != "" {
						t.Fatal(detached)
					}
				}
			}
			if got := registry.RemoveProject(ctx, ProjectTarget{view.Project.ID, view.Revision}); !got.Committed {
				t.Fatal(got)
			}
		} else if attach.Code != "project_unavailable" || !remove.Committed {
			t.Fatal(attach, remove)
		}
	}
}
