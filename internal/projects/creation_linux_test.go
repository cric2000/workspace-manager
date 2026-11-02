package projects

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

func creationFixture(t *testing.T) (*Service, CreationDraft) {
	t.Helper()
	service, _, _ := registryFixture(t)
	parent := t.TempDir()
	tool := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	// A fake executable that must never run: creation only generates files.
	t.Setenv("PATH", filepath.Dir(tool))
	return service, CreationDraft{TemplateID: "go", Name: "hello-go", ParentDirectory: parent, ModulePath: "example.com/you/hello-go"}
}
func creationReview(t *testing.T, service *Service, draft CreationDraft) CreationReview {
	t.Helper()
	result := service.ReviewCreation(context.Background(), draft)
	if result.Code != "" || result.Review == nil {
		t.Fatal(result)
	}
	return *result.Review
}

func TestCreationReviewCommitReplayAndReopen(t *testing.T) {
	service, draft := creationFixture(t)
	ctx := context.Background()
	review := creationReview(t, service, draft)
	if outcome := service.GetCreationOutcome(ctx, review.Token); outcome.Code != "creation_pending" || outcome.Created {
		t.Fatal(outcome)
	}
	entries, _ := os.ReadDir(draft.ParentDirectory)
	if len(entries) != 0 {
		t.Fatal("preview wrote files")
	}
	if len(service.ListProjects(ctx).State.Projects) != 0 {
		t.Fatal("preview registered project")
	}
	file := review.Plan.Files[0]
	review.Plan.Files[0].Contents = "tampered"
	review.Startup.Arguments[0] = "tampered"
	result := service.CommitCreation(ctx, review.Token)
	if result.Code != "" || !result.Created || !result.Registered || result.State == nil || len(result.Logs) != 3 {
		t.Fatal(result)
	}
	data, err := os.ReadFile(filepath.Join(result.Path, file.Path))
	if err != nil || string(data) != file.Contents {
		t.Fatal("client changed held files", err)
	}
	project := result.State.Projects[0].Project
	if project.Source != domain.SourceBootstrapped || len(project.Commands) != 1 || project.Commands[0].Approval != nil || !reflect.DeepEqual(project.Commands[0].Configuration.Arguments, []string{"run", "."}) {
		t.Fatal(project)
	}
	result.Logs[0] = "client mutation"
	result.State.Projects[0].Project.Name = "client mutation"
	replay := service.CommitCreation(ctx, review.Token)
	if replay.Code != "" || replay.State.Projects[0].Project.Name != draft.Name || replay.Logs[0] == "client mutation" {
		t.Fatal(replay)
	}
	if checked := service.GetCreationOutcome(ctx, review.Token); !reflect.DeepEqual(replay, checked) {
		t.Fatal("outcome query changed saved result", checked)
	}
	records, _ := os.ReadDir(draft.ParentDirectory)
	if len(records) != 1 {
		t.Fatal("replay created extra directories", records)
	}
	if err := service.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	reopened := NewService(nil)
	reopened.paths = service.paths
	defer reopened.ServiceShutdown()
	loaded := reopened.ListProjects(ctx)
	if loaded.Code != "" || !reflect.DeepEqual(loaded.State.Projects[0].Project, project) {
		t.Fatal(loaded)
	}
	if got := reopened.CommitCreation(ctx, review.Token); got.Created || got.Code != "review_changed" {
		t.Fatal(got)
	}
}

func TestRegistrationFailureAfterPublicationKeepsFilesAndCachesOutcome(t *testing.T) {
	service, draft := creationFixture(t)
	review := creationReview(t, service, draft)
	// Fail registration after its original valid configuration was checkpointed.
	service.creationLogStart = func(ctx context.Context, paths storage.Paths, log domain.CreationLog) (creationLogWriter, error) {
		writer, err := startCreationLog(ctx, paths, log)
		service.creations[review.Token].project.Source = "unsupported"
		return writer, err
	}
	result := service.CommitCreation(context.Background(), review.Token)
	if !result.Created || result.Registered || result.Code != "registration_failed" {
		t.Fatal(result)
	}
	for _, file := range review.Plan.Files {
		data, err := os.ReadFile(filepath.Join(review.Path, file.Path))
		if err != nil || string(data) != file.Contents {
			t.Fatal("partial outcome lost source", err)
		}
	}
	if replay := service.CommitCreation(context.Background(), review.Token); !reflect.DeepEqual(result, replay) {
		t.Fatal("partial outcome not replayed", replay)
	}
	if len(service.ListProjects(context.Background()).State.Projects) != 0 {
		t.Fatal("invalid registration persisted")
	}
}

func TestCreationRejectsChangedTargetParentToolExpiredReviewAndCancellation(t *testing.T) {
	for _, scenario := range []string{"target", "parent", "tool", "expired", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			service, draft := creationFixture(t)
			review := creationReview(t, service, draft)
			ctx := context.Background()
			switch scenario {
			case "target":
				os.Mkdir(review.Path, 0700)
				os.WriteFile(filepath.Join(review.Path, "user.txt"), []byte("keep"), 0600)
			case "parent":
				os.Rename(draft.ParentDirectory, draft.ParentDirectory+"-old")
				os.Mkdir(draft.ParentDirectory, 0700)
			case "tool":
				os.WriteFile(review.Startup.Executable, []byte("changed"), 0700)
			case "expired":
				service.now = func() time.Time { return review.ExpiresAt.Add(time.Second) }
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			result := service.CommitCreation(ctx, review.Token)
			if result.Created || result.Registered || result.Code == "" {
				t.Fatal(result)
			}
			if len(service.ListProjects(context.Background()).State.Projects) != 0 {
				t.Fatal("failed creation registered")
			}
			if scenario == "target" {
				data, _ := os.ReadFile(filepath.Join(review.Path, "user.txt"))
				if string(data) != "keep" {
					t.Fatal("user file changed")
				}
			} else if _, err := os.Stat(review.Path); !os.IsNotExist(err) {
				t.Fatal("failed creation published", err)
			}
		})
	}
}

func TestTwoCreationReviewsForSameDestinationHaveOneWinner(t *testing.T) {
	service, draft := creationFixture(t)
	other := NewService(nil)
	other.paths = service.paths
	defer other.ServiceShutdown()
	first := creationReview(t, service, draft)
	second := creationReview(t, other, draft)
	var a, b CreationResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); a = service.CommitCreation(context.Background(), first.Token) }()
	go func() { defer wg.Done(); b = other.CommitCreation(context.Background(), second.Token) }()
	wg.Wait()
	if a.Created == b.Created || a.Registered == b.Registered {
		t.Fatal(a, b)
	}
	projects := service.ListProjects(context.Background())
	if projects.Code != "" || len(projects.State.Projects) != 1 {
		t.Fatal(projects)
	}
}

func TestCreationRequiresValidTemplateOptionsAndExecutable(t *testing.T) {
	service, draft := creationFixture(t)
	draft.Name = "../escape"
	if got := service.ReviewCreation(context.Background(), draft); got.Code != "validation" {
		t.Fatal(got)
	}
	draft.Name = "safe"
	t.Setenv("PATH", t.TempDir())
	if got := service.ReviewCreation(context.Background(), draft); got.Code != "executable" {
		t.Fatal(got)
	}
}

func nodeCreationFixture(t *testing.T) (*Service, CreationDraft) {
	service, draft := creationFixture(t)
	draft.TemplateID = "node-typescript"
	draft.Name = "hello-node"
	draft.ModulePath = ""
	for _, program := range []string{"node", "npm"} {
		if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), program), []byte("#!/bin/sh\ntouch "+filepath.Join(draft.ParentDirectory, "must-not-run")+"\nexit 99\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return service, draft
}

func TestNodeCreationStoresNestedFilesAndUnapprovedRuntimeWithoutInstalling(t *testing.T) {
	service, draft := nodeCreationFixture(t)
	review := creationReview(t, service, draft)
	if review.DependencySetup == nil || len(service.creations[review.Token].executables) != 2 {
		t.Fatal(review)
	}
	review.DependencySetup.Arguments[0] = "tamper"
	review.Plan.DependencySetup.Arguments[0] = "tamper"
	if service.creations[review.Token].review.DependencySetup.Arguments[0] != "install" || service.creations[review.Token].review.Plan.DependencySetup.Arguments[0] != "install" {
		t.Fatal("client changed held setup")
	}
	result := service.CommitCreation(context.Background(), review.Token)
	if result.Code != "" || !result.Created || !result.Registered {
		t.Fatal(result)
	}
	project := result.State.Projects[0].Project
	if project.Source != domain.SourceBootstrapped || project.Commands[0].Approval != nil || !reflect.DeepEqual(project.Commands[0].Configuration.Arguments, []string{"--watch", "src/index.ts"}) {
		t.Fatal(project)
	}
	if _, err := os.Stat(filepath.Join(result.Path, "src/index.ts")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node_modules", "package-lock.json", "dist"} {
		if _, err := os.Stat(filepath.Join(result.Path, name)); !os.IsNotExist(err) {
			t.Fatal("unexpected install/build side effect", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(draft.ParentDirectory, "must-not-run")); !os.IsNotExist(err) {
		t.Fatal("executed template tool", err)
	}
	if replay := service.GetCreationOutcome(context.Background(), review.Token); !reflect.DeepEqual(result, replay) {
		t.Fatal("outcome changed", replay)
	}
}

func TestNodeCreationChecksAllRequiredToolsBeforeReviewAndCommit(t *testing.T) {
	for _, scenario := range []string{"missing-node", "missing-npm", "changed-npm"} {
		t.Run(scenario, func(t *testing.T) {
			service, draft := nodeCreationFixture(t)
			if scenario == "changed-npm" {
				review := creationReview(t, service, draft)
				if err := os.WriteFile(review.DependencySetup.Executable, []byte("changed"), 0700); err != nil {
					t.Fatal(err)
				}
				if got := service.CommitCreation(context.Background(), review.Token); got.Code != "review_changed" || got.Created {
					t.Fatal(got)
				}
			} else {
				program := "node"
				if scenario == "missing-npm" {
					program = "npm"
				}
				if err := os.Remove(filepath.Join(os.Getenv("PATH"), program)); err != nil {
					t.Fatal(err)
				}
				if got := service.ReviewCreation(context.Background(), draft); got.Code != "executable" {
					t.Fatal(got)
				}
			}
			entries, _ := os.ReadDir(draft.ParentDirectory)
			if len(entries) != 0 {
				t.Fatal("failed preflight wrote files", entries)
			}
		})
	}
}

func pythonCreationFixture(t *testing.T) (*Service, CreationDraft) {
	service, draft := creationFixture(t)
	draft.TemplateID = "python"
	draft.Name = "hello-python"
	draft.ModulePath = ""
	tool := filepath.Join(os.Getenv("PATH"), "python3")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\ntouch "+filepath.Join(draft.ParentDirectory, "must-not-run")+"\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return service, draft
}

func TestPythonCreationStoresFilesWithoutRunningPythonOrCreatingEnvironment(t *testing.T) {
	service, draft := pythonCreationFixture(t)
	review := creationReview(t, service, draft)
	if review.DependencySetup == nil || !reflect.DeepEqual(review.DependencySetup.Arguments, []string{"-m", "venv", ".venv"}) {
		t.Fatal(review)
	}
	review.Plan.DependencySetup.Label = "client mutation"
	review.DependencySetup.Arguments[2] = "outside"
	if service.creations[review.Token].review.Plan.DependencySetup.Label != "Optional virtual environment" || service.creations[review.Token].review.DependencySetup.Arguments[2] != ".venv" {
		t.Fatal("client mutated held setup")
	}
	result := service.CommitCreation(context.Background(), review.Token)
	if result.Code != "" || !result.Created || !result.Registered {
		t.Fatal(result)
	}
	project := result.State.Projects[0].Project
	if project.Source != domain.SourceBootstrapped || project.Commands[0].Approval != nil || !reflect.DeepEqual(project.Commands[0].Configuration.Arguments, []string{"-B", "main.py"}) {
		t.Fatal(project)
	}
	for _, path := range []string{"pyproject.toml", "main.py", "tests/test_main.py", "README.md", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(result.Path, path)); err != nil {
			t.Fatal(path, err)
		}
	}
	for _, path := range []string{".venv", "__pycache__", "tests/__pycache__", "build", "dist"} {
		if _, err := os.Stat(filepath.Join(result.Path, path)); !os.IsNotExist(err) {
			t.Fatal("creation ran setup/build/source", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(draft.ParentDirectory, "must-not-run")); !os.IsNotExist(err) {
		t.Fatal("creation executed Python", err)
	}
	if replay := service.GetCreationOutcome(context.Background(), review.Token); !reflect.DeepEqual(result, replay) {
		t.Fatal("outcome changed", replay)
	}
}

func TestPythonCreationRejectsInvalidNamesAndMissingOrChangedInterpreter(t *testing.T) {
	for _, scenario := range []string{"name", "missing", "changed"} {
		t.Run(scenario, func(t *testing.T) {
			service, draft := pythonCreationFixture(t)
			switch scenario {
			case "name":
				draft.Name = "trailing-"
				if got := service.ReviewCreation(context.Background(), draft); got.Code != "validation" {
					t.Fatal(got)
				}
			case "missing":
				if err := os.Remove(filepath.Join(os.Getenv("PATH"), "python3")); err != nil {
					t.Fatal(err)
				}
				if got := service.ReviewCreation(context.Background(), draft); got.Code != "executable" {
					t.Fatal(got)
				}
			case "changed":
				review := creationReview(t, service, draft)
				if err := os.WriteFile(review.Startup.Executable, []byte("changed"), 0700); err != nil {
					t.Fatal(err)
				}
				if got := service.CommitCreation(context.Background(), review.Token); got.Code != "review_changed" || got.Created {
					t.Fatal(got)
				}
			}
			entries, _ := os.ReadDir(draft.ParentDirectory)
			if len(entries) != 0 {
				t.Fatal("failed preflight changed source", entries)
			}
		})
	}
}

func TestFastAPICreationSavesUnapprovedLocalInterpreterAndHealthSettings(t *testing.T) {
	service, draft := pythonCreationFixture(t)
	draft.TemplateID, draft.Name = "fastapi", "hello-api"
	review := creationReview(t, service, draft)
	local := filepath.Join(review.Path, ".venv", "bin", "python")
	if review.Startup.Executable != local || review.Startup.Runtime == nil || review.Startup.Runtime.Readiness.URL != "http://127.0.0.1:8000/health" || review.DependencySetup == nil || len(review.AdditionalSetup) != 1 || review.AdditionalSetup[0].Executable != local {
		t.Fatal(review)
	}
	// Every returned nested object is detached from the held review/project.
	review.Startup.Runtime.Readiness.URL = "http://127.0.0.1:9999/changed"
	review.Plan.Runtime.Stop.Signal = "SIGINT"
	review.Plan.AdditionalSetup[0].Arguments[0] = "changed"
	review.AdditionalSetup[0].Arguments[0] = "changed"
	review.Plan.Files[0].Contents = "changed"
	held := service.creations[review.Token]
	if held.review.Plan.Runtime.Stop.Signal != "SIGTERM" || held.review.Plan.AdditionalSetup[0].Arguments[0] != "-m" || held.review.AdditionalSetup[0].Arguments[0] != "-m" || held.project.Commands[0].Configuration.Runtime.Readiness.URL != "http://127.0.0.1:8000/health" {
		t.Fatal("review aliases held plan")
	}
	result := service.CommitCreation(context.Background(), review.Token)
	if result.Code != "" || !result.Created || !result.Registered {
		t.Fatal(result)
	}
	project := result.State.Projects[0].Project
	if project.Source != domain.SourceBootstrapped || project.Commands[0].Approval != nil || project.Commands[0].Configuration.Executable != local || project.Preferences == nil || project.Preferences.Approval != nil || project.Preferences.PreviewURL != "http://127.0.0.1:8000/docs" {
		t.Fatal(project)
	}
	for _, name := range []string{".venv", ".git", "app/__pycache__", "must-not-run"} {
		if _, err := os.Stat(filepath.Join(result.Path, name)); !os.IsNotExist(err) {
			t.Fatal("creation ran setup or source", name, err)
		}
	}
	// Until manual environment setup, the saved command cannot be approved.
	edit := editDraft(result.State.Projects[0])
	edit.Preferences = &PreferencesDraft{PreviewURL: project.Preferences.PreviewURL}
	if got := service.ReviewRegistration(context.Background(), edit); got.Code != "executable" {
		t.Fatal(got)
	}
	for _, file := range held.review.Plan.Files {
		contents, err := os.ReadFile(filepath.Join(result.Path, file.Path))
		if err != nil || string(contents) != file.Contents {
			t.Fatal(file.Path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(draft.ParentDirectory, "must-not-run")); !os.IsNotExist(err) {
		t.Fatal("Python executed during creation", err)
	}
}
