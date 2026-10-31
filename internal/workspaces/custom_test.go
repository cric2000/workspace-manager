package workspaces

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"workspace-manager/internal/applications"
	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

func TestCustomConfigurationSaveDuplicateResolveAndReopen(t *testing.T) {
	ctx := context.Background()
	paths := storage.Paths{ConfigDir: filepath.Join(t.TempDir(), "config")}
	service := testService(t, paths)
	view := ok(t, service.Create(ctx, Draft{"Coding", ""})).Workspaces[0]
	custom := &domain.CustomApplication{Name: "Offline tool", Executable: "/not-installed/tool", Arguments: []string{"two words", ""}, WorkingDirectory: "/offline/project"}
	view = ok(t, service.SetApplications(ctx, target(view), []domain.WorkspaceApplication{{Custom: custom, DuplicatePolicy: domain.SkipIfRunning}})).Workspaces[0]
	if custom.ID != "" || view.Applications[0].Custom.ID.Validate() != nil {
		t.Fatal("server-owned ID missing or input mutated")
	}
	request := applications.CustomTarget{WorkspaceID: view.ID, Revision: view.Revision, CustomID: view.Applications[0].Custom.ID}
	resolve := NewCustomResolver(service)
	resolved, err := resolve(ctx, request)
	if err != nil || !reflect.DeepEqual(resolved, *view.Applications[0].Custom) {
		t.Fatal(resolved, err)
	}
	resolved.Arguments[0] = "client copy"
	state := ok(t, service.Duplicate(ctx, target(view), Draft{"Copy", ""}))
	copy := state.Workspaces[1]
	selection := domain.CloneApplications(copy.Applications)
	selection[0].Custom.Arguments[0] = "independent copy"
	selection[0].Custom.Name = "Changed copy"
	ok(t, service.SetApplications(ctx, target(copy), selection))
	if got, err := resolve(ctx, request); err != nil || got.Arguments[0] != "two words" {
		t.Fatal(got, err)
	}
	view = ok(t, service.Edit(ctx, target(view), Draft{"Coding", "New revision"})).Workspaces[0]
	if _, err := resolve(ctx, request); !errors.Is(err, applications.ErrLaunchChanged) {
		t.Fatal("stale launch revision accepted", err)
	}
	request.Revision = view.Revision
	if _, err := resolve(ctx, request); err != nil {
		t.Fatal(err)
	}
	service.ServiceShutdown()
	reopened := testService(t, paths)
	if got, err := NewCustomResolver(reopened)(ctx, request); err != nil || got.Arguments[0] != "two words" {
		t.Fatal(got, err)
	}
}

func TestCustomDraftRejectsInventedIDsAndOversizedEncodingBeforeCommit(t *testing.T) {
	ctx := context.Background()
	service := testService(t, storage.Paths{ConfigDir: filepath.Join(t.TempDir(), "config")})
	view := ok(t, service.Create(ctx, Draft{"Coding", ""})).Workspaces[0]
	custom := domain.CustomApplication{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "Tool", Executable: "/bin/true", WorkingDirectory: "/tmp"}
	if got := service.SetApplications(ctx, target(view), []domain.WorkspaceApplication{{Custom: &custom, DuplicatePolicy: domain.SkipIfRunning}}); got.Code != "validation" || got.Committed {
		t.Fatal("caller assigned new identity", got)
	}
	custom.ID = ""
	custom.Arguments = []string{strings.Repeat("\x01", 8192), strings.Repeat("\x01", 8192), strings.Repeat("\x01", 8192), strings.Repeat("\x01", 8192)}
	choices := make([]domain.WorkspaceApplication, 64)
	for i := range choices {
		copy := custom
		choices[i] = domain.WorkspaceApplication{Custom: &copy, DuplicatePolicy: domain.SkipIfRunning}
	}
	if got := service.SetApplications(ctx, target(view), choices); got.Code != "validation" || got.Committed {
		t.Fatal("oversized encoded draft was not a precommit validation error", got)
	}
	current := ok(t, service.List(ctx)).Workspaces[0]
	if current.Revision != view.Revision || len(current.Applications) != 0 {
		t.Fatal("failed draft mutated the profile")
	}
}
