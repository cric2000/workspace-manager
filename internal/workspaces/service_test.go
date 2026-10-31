package workspaces

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

func testService(t *testing.T, paths storage.Paths) *Service {
	t.Helper()
	service := &Service{paths: func() (storage.Paths, error) { return paths, nil }}
	t.Cleanup(func() { _ = service.ServiceShutdown() })
	return service
}

func ok(t *testing.T, result Result) *State {
	t.Helper()
	if result.Code != "" || result.State == nil {
		t.Fatalf("unexpected result: %+v", result)
	}
	return result.State
}

func target(view View) Target { return Target{view.ID, view.Revision} }

func TestProfileWorkflowAndReopen(t *testing.T) {
	ctx := context.Background()
	paths := storage.Paths{ConfigDir: filepath.Join(t.TempDir(), "config")}
	service := testService(t, paths)
	if state := ok(t, service.List(ctx)); len(state.Workspaces) != 0 || state.DefaultWorkspaceID != "" {
		t.Fatal(state)
	}
	state := ok(t, service.Create(ctx, Draft{"  Coding  ", "API and frontend"}))
	original := state.Workspaces[0]
	if original.Name != "Coding" || original.ID.Validate() != nil || original.CreatedAt.IsZero() {
		t.Fatal(original)
	}
	projectID, _ := domain.NewID()
	if err := service.profiles.Update(ctx, original.ID, original.Revision, func(value domain.Workspace) (domain.Workspace, error) {
		value.ProjectIDs = []domain.ID{projectID}
		return value, nil
	}); err != nil {
		t.Fatal(err)
	}
	original = ok(t, service.List(ctx)).Workspaces[0]
	state = ok(t, service.Edit(ctx, target(original), Draft{"Backend", "Changed"}))
	edited := state.Workspaces[0]
	if edited.ProjectCount != 1 || edited.ID != original.ID || !edited.CreatedAt.Equal(original.CreatedAt) {
		t.Fatal(edited)
	}
	if stale := service.Duplicate(ctx, target(original), Draft{"Stale copy", ""}); stale.Code != "conflict" {
		t.Fatal(stale)
	}
	state = ok(t, service.Duplicate(ctx, target(edited), Draft{"Backend copy", edited.Description}))
	if len(state.Workspaces) != 2 {
		t.Fatal(state)
	}
	copy := state.Workspaces[1]
	if copy.ID == edited.ID || copy.ProjectCount != 1 || copy.Revision == edited.Revision {
		t.Fatal(copy)
	}
	stored, err := service.profiles.Get(ctx, copy.ID)
	if err != nil || stored.Value.ProjectIDs[0] != projectID {
		t.Fatal(stored, err)
	}
	state = ok(t, service.SetDefault(ctx, target(edited), state.SettingsRevision))
	if blocked := service.Delete(ctx, target(edited)); blocked.Code != "default_workspace" || blocked.Committed {
		t.Fatal(blocked)
	}
	if stale := service.Edit(ctx, target(original), Draft{"Lost update", ""}); stale.Code != "conflict" {
		t.Fatal(stale)
	}
	if stale := service.Delete(ctx, target(original)); stale.Code != "default_workspace" {
		t.Fatal(stale)
	}
	state = ok(t, service.SetDefault(ctx, Target{}, state.SettingsRevision))
	if stale := service.Delete(ctx, target(original)); stale.Code != "conflict" || stale.Committed {
		t.Fatal(stale)
	}
	state = ok(t, service.Delete(ctx, target(edited)))
	if len(state.Workspaces) != 1 || state.Workspaces[0].ID != copy.ID {
		t.Fatal(state)
	}
	// Deletion archives profile metadata, leaving shared project references alone.
	if history, err := service.profiles.History(ctx, edited.ID); err != nil || len(history) == 0 {
		t.Fatal(history, err)
	}
	if err := service.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	reopened := testService(t, paths)
	state = ok(t, reopened.List(ctx))
	if len(state.Workspaces) != 1 || state.Workspaces[0].ID != copy.ID || state.DefaultWorkspaceID != "" {
		t.Fatal(state)
	}
	if closed := service.List(ctx); closed.Code != "closed" {
		t.Fatal(closed)
	}
}

func TestValidationPreferencesAndCancellation(t *testing.T) {
	ctx := context.Background()
	service := testService(t, storage.Paths{ConfigDir: filepath.Join(t.TempDir(), "config")})
	ok(t, service.List(ctx))
	for _, draft := range []Draft{{"  ", ""}, {"Name\nInjected", ""}, {"Valid", "bad\x00description"}} {
		if result := service.Create(ctx, draft); result.Code != "validation" || result.Committed {
			t.Fatal(result)
		}
	}
	state := ok(t, service.Create(ctx, Draft{"Coding", ""}))
	workspace := state.Workspaces[0]
	oldSettings := state.SettingsRevision
	state = ok(t, service.SetDefault(ctx, target(workspace), state.SettingsRevision))
	if result := service.SetDefault(ctx, Target{}, oldSettings); result.Code != "conflict" {
		t.Fatal(result)
	}
	if err := service.settings.Update(ctx, state.SettingsRevision, func(value domain.Settings) (domain.Settings, error) {
		value.Autostart, value.Appearance, value.InitialDelaySeconds = true, domain.AppearanceDark, 27
		return value, nil
	}); err != nil {
		t.Fatal(err)
	}
	state = ok(t, service.List(ctx))
	if result := service.SetDefault(ctx, Target{}, state.SettingsRevision); result.Code != "default_required" {
		t.Fatal(result)
	}
	state = ok(t, service.Create(ctx, Draft{"Learning", ""}))
	state = ok(t, service.SetDefault(ctx, target(state.Workspaces[1]), state.SettingsRevision))
	settings, err := service.settings.Get(ctx)
	if err != nil || !settings.Value.Autostart || settings.Value.Appearance != domain.AppearanceDark || settings.Value.InitialDelaySeconds != 27 {
		t.Fatal(settings, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if result := service.Create(cancelled, Draft{"Cancelled", ""}); result.Code != "cancelled" || result.Committed {
		t.Fatal(result)
	}
	if len(ok(t, service.List(ctx)).Workspaces) != 2 {
		t.Fatal("cancelled create persisted")
	}
}

func TestDamagedSettingsArePreservedAndRetryable(t *testing.T) {
	ctx := context.Background()
	paths := storage.Paths{ConfigDir: filepath.Join(t.TempDir(), "config")}
	root, err := storage.OpenPrivateDirectory(paths.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	_ = root.Close()
	damaged := []byte("{not valid json containing private information}")
	if err := os.WriteFile(paths.SettingsFile(), damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	service := testService(t, paths)
	result := service.List(ctx)
	if result.Code != "invalid_storage" || result.State != nil {
		t.Fatal(result)
	}
	bytes, _ := os.ReadFile(paths.SettingsFile())
	if string(bytes) != string(damaged) {
		t.Fatal("damaged settings overwritten")
	}
	codec, _ := storage.NewCodec[domain.Settings](1, nil)
	valid, _ := codec.Encode(domain.DefaultSettings())
	if err := os.WriteFile(paths.SettingsFile(), valid, 0o600); err != nil {
		t.Fatal(err)
	}
	ok(t, service.List(ctx))
}

func TestConcurrentDefaultAndDeletionAcrossServiceInstances(t *testing.T) {
	ctx := context.Background()
	paths := storage.Paths{ConfigDir: filepath.Join(t.TempDir(), "config")}
	first, second := testService(t, paths), testService(t, paths)
	for i := 0; i < 12; i++ {
		state := ok(t, first.Create(ctx, Draft{"Racing profile", ""}))
		profile := state.Workspaces[0]
		var selected, deleted Result
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			selected = first.SetDefault(ctx, target(profile), state.SettingsRevision)
		}()
		go func() { defer wait.Done(); <-start; deleted = second.Delete(ctx, target(profile)) }()
		close(start)
		wait.Wait()
		current := ok(t, first.List(ctx))
		if current.DefaultMissing {
			t.Fatalf("dangling default: selected=%+v deleted=%+v", selected, deleted)
		}
		if selected.Code == "" {
			if deleted.Code != "default_workspace" {
				t.Fatal(deleted)
			}
			current = ok(t, first.SetDefault(ctx, Target{}, current.SettingsRevision))
			ok(t, first.Delete(ctx, target(current.Workspaces[0])))
		} else if selected.Code != "not_found" || deleted.Code != "" {
			t.Fatal(selected, deleted)
		}
	}
}

func TestCommittedFailureDoesNotInviteRetry(t *testing.T) {
	for _, err := range []error{
		&storage.MutationError{Committed: true, Err: errors.New("private path")},
		&storage.MutationError{Committed: true, Err: context.Canceled},
	} {
		result := failure(err)
		if result.Code != "durability_uncertain" || !result.Committed || result.State != nil {
			t.Fatal(result)
		}
	}
}
