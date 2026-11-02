package projects

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

func registryFixture(t *testing.T) (*Service, RegistrationDraft, storage.Paths) {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project with spaces")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(root, "tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\ntouch \""+filepath.Join(project, "must-not-run")+"\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	paths := storage.Paths{ConfigDir: filepath.Join(root, "config"), StateDir: filepath.Join(root, "state"), CacheDir: filepath.Join(root, "cache")}
	service := NewService(nil)
	service.paths = func() (storage.Paths, error) { return paths, nil }
	t.Cleanup(func() { _ = service.ServiceShutdown() })
	return service, RegistrationDraft{Name: "API", Path: project, Commands: []CommandDraft{{Configuration: domain.CommandConfiguration{Name: "Development", Executable: tool, Arguments: []string{"two words", "", "; $HOME %U"}, WorkingDirectory: project}}}}, paths
}

func requireReview(t *testing.T, service *Service, draft RegistrationDraft) RegistrationReview {
	t.Helper()
	result := service.ReviewRegistration(context.Background(), draft)
	if result.Code != "" || result.Review == nil {
		t.Fatal(result)
	}
	return *result.Review
}

func requireSave(t *testing.T, service *Service, review RegistrationReview) RegistryResult {
	t.Helper()
	result := service.CommitRegistration(context.Background(), review.Token)
	if result.Code != "" || !result.Committed || result.State == nil {
		t.Fatal(result)
	}
	return result
}

func editDraft(view ProjectView) RegistrationDraft {
	draft := RegistrationDraft{ID: view.Project.ID, Revision: view.Revision, Name: view.Project.Name, Path: view.Project.Path}
	for _, command := range view.Project.Commands {
		draft.Commands = append(draft.Commands, CommandDraft{ID: command.ID, Configuration: command.Configuration})
	}
	return draft
}

func TestReviewedRegistrationPersistsWithoutExecutionAndReplaysOnce(t *testing.T) {
	service, draft, paths := registryFixture(t)
	alias := filepath.Join(filepath.Dir(draft.Path), "alias")
	if err := os.Symlink(draft.Path, alias); err != nil {
		t.Fatal(err)
	}
	draft.Path = alias
	review := requireReview(t, service, draft)
	if review.Project.Path == alias || review.Project.Commands[0].Approval != nil {
		t.Fatal("canonicalization or premature approval", review)
	}
	files, _ := filepath.Glob(filepath.Join(paths.ProjectsDir(), "*.json"))
	if len(files) != 0 {
		t.Fatal("review saved a record")
	}
	review.Project.Commands[0].Configuration.Arguments[0] = "tampered client response"
	result := requireSave(t, service, review)
	project := cloneProject(result.State.Projects[0].Project)
	if project.Validate() != nil || project.Commands[0].Approval == nil || !reflect.DeepEqual(project.Commands[0].Configuration.Arguments, draft.Commands[0].Configuration.Arguments) {
		t.Fatal(project)
	}
	filename := filepath.Join(paths.ProjectsDir(), string(project.ID)+".json")
	before, err := os.ReadFile(filename)
	if err != nil || !bytes.Contains(before, []byte(`"schemaVersion": 3`)) {
		t.Fatal(err)
	}
	result.State.Projects[0].Project.Commands[0].Configuration.Arguments[0] = "mutated result"
	replay := requireSave(t, service, review)
	after, _ := os.ReadFile(filename)
	if !bytes.Equal(before, after) || replay.State.Projects[0].Project.Commands[0].Configuration.Arguments[0] != "two words" {
		t.Fatal("replay changed storage or returned aliased state")
	}
	history, err := service.registry.History(context.Background(), project.ID)
	if err != nil || len(history) != 1 || history[0] != replay.State.Projects[0].Revision {
		t.Fatal("replay wrote history", history, err)
	}
	if _, err := os.Stat(filepath.Join(project.Path, "must-not-run")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("command executed", err)
	}
	if err := service.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	reopened := NewService(nil)
	reopened.paths = service.paths
	defer reopened.ServiceShutdown()
	loaded := reopened.ListProjects(context.Background())
	if loaded.Code != "" || !reflect.DeepEqual(loaded.State.Projects[0].Project, project) {
		t.Fatal(loaded)
	}
	if replay := reopened.CommitRegistration(context.Background(), review.Token); replay.Code != "review_changed" || replay.Committed {
		t.Fatal("tokens survived service restart", replay)
	}
}

func TestEditingRequiresFreshRevisionAndArchivesExactApproval(t *testing.T) {
	service, draft, paths := registryFixture(t)
	first := requireSave(t, service, requireReview(t, service, draft)).State.Projects[0]
	filename := filepath.Join(paths.ProjectsDir(), string(first.Project.ID)+".json")
	oldBytes, _ := os.ReadFile(filename)
	draft = editDraft(first)
	draft.Name = "Changed name"
	draft.Commands[0].Configuration.Arguments = []string{"new", ""}
	left := requireReview(t, service, draft)
	right := requireReview(t, service, draft)
	if left.Project.Commands[0].Approval != nil {
		t.Fatal("editing carried old approval")
	}
	saved := requireSave(t, service, left).State.Projects[0]
	if saved.Project.ID != first.Project.ID || saved.Project.Commands[0].ID != first.Project.Commands[0].ID || saved.Project.Commands[0].Approval.Fingerprint == first.Project.Commands[0].Approval.Fingerprint {
		t.Fatal(saved)
	}
	if stale := service.CommitRegistration(context.Background(), right.Token); stale.Code != "conflict" || stale.Committed {
		t.Fatal(stale)
	}
	if stale := service.ReviewRegistration(context.Background(), draft); stale.Code != "conflict" {
		t.Fatal(stale)
	}
	archive, err := os.ReadFile(filepath.Join(paths.ProjectsDir(), "history", string(first.Project.ID), string(first.Revision)+".json"))
	if err != nil || !bytes.Equal(oldBytes, archive) {
		t.Fatal("approval history lost", err)
	}
	draft = editDraft(saved)
	draft.Commands = nil
	removed := requireSave(t, service, requireReview(t, service, draft)).State.Projects[0]
	if len(removed.Project.Commands) != 0 {
		t.Fatal("removing commands failed")
	}
}

func TestCompetingServicesCannotRegisterSameCanonicalDirectory(t *testing.T) {
	service, draft, _ := registryFixture(t)
	other := NewService(nil)
	other.paths = service.paths
	defer other.ServiceShutdown()
	left := requireReview(t, service, draft)
	alias := filepath.Join(filepath.Dir(draft.Path), "alias")
	if err := os.Symlink(draft.Path, alias); err != nil {
		t.Fatal(err)
	}
	draft.Path = alias
	right := requireReview(t, other, draft)
	results := make(chan RegistryResult, 2)
	var group sync.WaitGroup
	group.Go(func() { results <- service.CommitRegistration(context.Background(), left.Token) })
	group.Go(func() { results <- other.CommitRegistration(context.Background(), right.Token) })
	group.Wait()
	close(results)
	committed, duplicate := 0, 0
	for result := range results {
		if result.Committed {
			committed++
		}
		if result.Code == "duplicate" {
			duplicate++
		}
	}
	if committed != 1 || duplicate != 1 {
		t.Fatal(committed, duplicate)
	}
	if result := service.ListProjects(context.Background()); result.Code != "" || len(result.State.Projects) != 1 {
		t.Fatal(result)
	}
}

func TestReviewResolvesPATHWithoutExecutingOrFreezingSource(t *testing.T) {
	service, draft, _ := registryFixture(t)
	tool := draft.Commands[0].Configuration.Executable
	t.Setenv("PATH", filepath.Dir(tool))
	draft.Commands[0].Configuration.Executable = filepath.Base(tool)
	review := requireReview(t, service, draft)
	if review.Project.Commands[0].Configuration.Executable != tool {
		t.Fatal("review did not resolve PATH")
	}
	// Approval covers argv, not source contents; normal directory activity must
	// not make an otherwise unchanged reviewed configuration impossible to save.
	if err := os.WriteFile(filepath.Join(draft.Path, "source.txt"), []byte("changed after inspection"), 0o600); err != nil {
		t.Fatal(err)
	}
	requireSave(t, service, review)
	if _, err := os.Stat(filepath.Join(draft.Path, "must-not-run")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("PATH resolution executed tool")
	}
}

func TestChangedExecutableDirectoryExpiryAndDraftValidation(t *testing.T) {
	for _, scenario := range []string{"executable", "directory", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			service, draft, _ := registryFixture(t)
			review := requireReview(t, service, draft)
			switch scenario {
			case "executable":
				if err := os.WriteFile(draft.Commands[0].Configuration.Executable, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Rename(draft.Path, draft.Path+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(draft.Path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "expired":
				service.now = func() time.Time { return review.ExpiresAt }
			}
			result := service.CommitRegistration(context.Background(), review.Token)
			if result.Code != "review_changed" || result.Committed {
				t.Fatal(result)
			}
		})
	}
	for _, test := range []struct {
		change func(*RegistrationDraft)
		code   string
	}{
		{func(d *RegistrationDraft) { d.Commands[0].Configuration.Executable = "missing-workspace-test-tool" }, "executable"},
		{func(d *RegistrationDraft) { d.Commands[0].Configuration.Executable = "./relative-tool" }, "executable"},
		{func(d *RegistrationDraft) { d.Commands[0].Configuration.WorkingDirectory = filepath.Dir(d.Path) }, "directory"},
		{func(d *RegistrationDraft) { d.Name = "bad\nname" }, "validation"},
		{func(d *RegistrationDraft) { d.Commands[0].ID = "11111111111111111111111111111111" }, "validation"},
		{func(d *RegistrationDraft) { d.Commands[0].Configuration.Arguments = []string{"\x00"} }, "validation"},
	} {
		service, draft, _ := registryFixture(t)
		test.change(&draft)
		if got := service.ReviewRegistration(context.Background(), draft); got.Code != test.code || got.Review != nil {
			t.Fatal(got, test.code)
		}
	}
}

func TestRegistryFailClosedAndBoundedReviews(t *testing.T) {
	service, draft, paths := registryFixture(t)
	draft.Commands = nil
	review := requireReview(t, service, draft)
	if got := service.CommitRegistration(context.Background(), "invented"); got.Code != "review_changed" {
		t.Fatal(got)
	}
	for len(service.reviews) < 64 {
		requireReview(t, service, draft)
	}
	if got := service.ReviewRegistration(context.Background(), draft); got.Code != "busy" {
		t.Fatal(got)
	}
	expiredTime := review.ExpiresAt.Add(time.Second)
	service.now = func() time.Time { return expiredTime }
	review = requireReview(t, service, draft)
	if len(service.reviews) != 1 {
		t.Fatal("expired tokens retained")
	}
	if err := os.WriteFile(filepath.Join(paths.ProjectsDir(), "11111111111111111111111111111111.json"), []byte(`{"schemaVersion":999,"kind":"project","data":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := service.CommitRegistration(context.Background(), review.Token); got.Code != "newer_schema" || got.Committed {
		t.Fatal(got)
	}
	if got := service.ListProjects(context.Background()); got.Code != "newer_schema" || got.State != nil {
		t.Fatal(got)
	}
	if err := service.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	if got := service.ListProjects(context.Background()); got.Code != "closed" {
		t.Fatal(got)
	}
	if message := registryFailure(errors.New("secret=token /private/path")).Message; strings.Contains(message, "secret=") || strings.Contains(message, "/private") {
		t.Fatal("private error exposed")
	}
}

func TestDirectoryChooserCancellationErrorsAndSingleDialog(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	service := NewService(nil, WithDirectoryPicker(func() (string, error) { close(entered); <-release; return "/selected", nil }))
	result := make(chan DirectoryResult)
	go func() { result <- service.BrowseDirectory(context.Background()) }()
	<-entered
	if got := service.BrowseDirectory(context.Background()); got.Code != "busy" {
		t.Fatal(got)
	}
	close(release)
	if got := <-result; got.Path != "/selected" {
		t.Fatal(got)
	}
	for _, test := range []struct {
		path string
		err  error
		code string
	}{{"", nil, "cancelled"}, {"", errors.New("private"), "chooser"}} {
		service := NewService(nil, WithDirectoryPicker(func() (string, error) { return test.path, test.err }))
		if got := service.BrowseDirectory(context.Background()); got.Code != test.code {
			t.Fatal(got)
		}
	}
}
