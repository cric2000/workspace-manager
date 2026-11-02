package projects

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/projects/bootstrap"
)

func createdForSetup(t *testing.T, template string) (*Service, CreationReview) {
	t.Helper()
	var service *Service
	var draft CreationDraft
	switch template {
	case "python", "fastapi":
		service, draft = pythonCreationFixture(t)
		draft.TemplateID = template
	case "node-typescript":
		service, draft = nodeCreationFixture(t)
	default:
		service, draft = creationFixture(t)
	}
	review := creationReview(t, service, draft)
	result := service.CommitCreation(context.Background(), review.Token)
	if result.Code != "" || !result.Registered || !result.Created {
		t.Fatal(result)
	}
	for _, tool := range []string{"git", "node", "npm"} {
		path := filepath.Join(os.Getenv("PATH"), tool)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	return service, review
}
func requireSetup(t *testing.T, service *Service, creation CreationReview, kind string) SetupReview {
	t.Helper()
	result := service.ReviewSetup(context.Background(), creation.Token, kind)
	if result.Code != "" || result.Review == nil {
		t.Fatal(result)
	}
	return *result.Review
}

func TestGitSetupNeedsSeparateApprovalPreservesSourceAndReplays(t *testing.T) {
	service, creation := createdForSetup(t, "go")
	calls := 0
	service.setupRunner = func(_ context.Context, action domain.CommandAction, _ string, _ *os.File) bootstrap.CommandResult {
		calls++
		if !reflect.DeepEqual(action.Arguments, []string{"-c", "core.hooksPath=/dev/null", "init", "--initial-branch=main", "--template=", "--", "."}) {
			t.Fatal(action)
		}
		if err := os.Mkdir(filepath.Join(action.WorkingDirectory, ".git"), 0700); err != nil {
			t.Fatal(err)
		}
		return bootstrap.CommandResult{Started: true, ExitCode: 0, Output: "initialized"}
	}
	review := requireSetup(t, service, creation, "git")
	if calls != 0 {
		t.Fatal("review executed setup")
	}
	if queried := service.GetSetupOutcome(context.Background(), review.Token); queried.Code != "setup_pending" || calls != 0 {
		t.Fatal(queried)
	}
	review.Action.Arguments[0] = "tampered"
	result := service.CommitSetup(context.Background(), review.Token)
	if !result.Finished || !result.Started || !result.Succeeded || result.Output != "initialized" || calls != 1 {
		t.Fatal(result, calls)
	}
	if replay := service.CommitSetup(context.Background(), review.Token); replay != result || calls != 1 {
		t.Fatal("setup replay executed", replay)
	}
	if queried := service.GetSetupOutcome(context.Background(), review.Token); queried != result {
		t.Fatal(queried)
	}
	if duplicate := service.ReviewSetup(context.Background(), creation.Token, "git"); duplicate.Code != "setup_attempted" {
		t.Fatal(duplicate)
	}
	data, err := os.ReadFile(filepath.Join(creation.Path, "main.go"))
	if err != nil || !strings.Contains(string(data), "Hello from") {
		t.Fatal("source changed", err)
	}
	project := service.ListProjects(context.Background()).State.Projects[0].Project
	if project.Commands[0].Approval != nil || project.Source != domain.SourceBootstrapped {
		t.Fatal("setup approved startup", project)
	}
}

func TestSetupRejectsChangedInputsOutputsToolsDirectoryAndExpiry(t *testing.T) {
	for _, scenario := range []string{"source", "added-file", "existing-output", "symlink-output", "source-link", "tool", "directory", "expired", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			service, creation := createdForSetup(t, "node-typescript")
			calls := 0
			service.setupRunner = func(context.Context, domain.CommandAction, string, *os.File) bootstrap.CommandResult {
				calls++
				return bootstrap.CommandResult{Started: true, ExitCode: 0}
			}
			review := requireSetup(t, service, creation, "dependencies")
			ctx := context.Background()
			switch scenario {
			case "source":
				if err := os.WriteFile(filepath.Join(creation.Path, "package.json"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "added-file":
				if err := os.WriteFile(filepath.Join(creation.Path, ".npmrc"), []byte("registry=unreviewed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "existing-output":
				if err := os.Mkdir(filepath.Join(creation.Path, "node_modules"), 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink-output":
				if err := os.Symlink(t.TempDir(), filepath.Join(creation.Path, "node_modules")); err != nil {
					t.Fatal(err)
				}
			case "source-link":
				target := filepath.Join(t.TempDir(), "external")
				if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(creation.Path, "package.json")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(creation.Path, "package.json")); err != nil {
					t.Fatal(err)
				}
			case "tool":
				if err := os.WriteFile(review.Action.Executable, []byte("changed"), 0700); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Rename(creation.Path, creation.Path+"-moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(creation.Path, 0700); err != nil {
					t.Fatal(err)
				}
			case "expired":
				service.now = func() time.Time { return review.ExpiresAt.Add(time.Second) }
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			result := service.CommitSetup(ctx, review.Token)
			if result.Code == "" || result.Started || !result.Finished || calls != 0 {
				t.Fatal(result, calls)
			}
		})
	}
}

func TestSetupCancellationKeepsPartialOutputAndQueriesWithoutRetry(t *testing.T) {
	service, creation := createdForSetup(t, "go")
	entered := make(chan struct{})
	var calls atomic.Int32
	service.setupRunner = func(ctx context.Context, action domain.CommandAction, _ string, _ *os.File) bootstrap.CommandResult {
		calls.Add(1)
		_ = os.Mkdir(filepath.Join(action.WorkingDirectory, ".git"), 0700)
		close(entered)
		<-ctx.Done()
		return bootstrap.CommandResult{Started: true, ExitCode: -1, Err: ctx.Err(), Output: "partial output"}
	}
	review := requireSetup(t, service, creation, "git")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan SetupResult, 1)
	go func() { finished <- service.CommitSetup(ctx, review.Token) }()
	<-entered
	if result := service.CommitSetup(context.Background(), review.Token); !result.Running || calls.Load() != 1 {
		t.Fatal(result)
	}
	if result := service.GetSetupOutcome(context.Background(), review.Token); !result.Running || calls.Load() != 1 {
		t.Fatal(result)
	}
	if loaded := service.ListProjects(context.Background()); loaded.Code != "" {
		t.Fatal(loaded)
	}
	cancel()
	result := <-finished
	if result.Succeeded || !result.Started || !result.Finished || result.Code != "cancelled" || !strings.Contains(result.Message, "Partial setup files were kept") {
		t.Fatal(result)
	}
	if _, err := os.Stat(filepath.Join(creation.Path, ".git")); err != nil {
		t.Fatal("partial output lost")
	}
	if replay := service.CommitSetup(context.Background(), review.Token); replay != result || calls.Load() != 1 {
		t.Fatal(replay)
	}
	if duplicate := service.ReviewSetup(context.Background(), creation.Token, "git"); duplicate.Code != "setup_attempted" {
		t.Fatal(duplicate)
	}
}

func TestRealGitInitializationIgnoresCallerGitEnvironment(t *testing.T) {
	original := os.Getenv("PATH")
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git unavailable")
	}
	service, creation := createdForSetup(t, "go")
	t.Setenv("PATH", original)
	external := t.TempDir()
	t.Setenv("GIT_DIR", external)
	t.Setenv("GIT_WORK_TREE", external)
	review := requireSetup(t, service, creation, "git")
	if _, err := os.Stat(filepath.Join(creation.Path, ".git")); !os.IsNotExist(err) {
		t.Fatal("review initialized Git")
	}
	result := service.CommitSetup(context.Background(), review.Token)
	if !result.Succeeded || !result.Started {
		t.Fatal(result)
	}
	head, err := os.ReadFile(filepath.Join(creation.Path, ".git/HEAD"))
	if err != nil || string(head) != "ref: refs/heads/main\n" {
		t.Fatal(string(head), err)
	}
	for _, name := range []string{".git/index", ".git/hooks", ".git/refs/heads/main"} {
		if _, err := os.Stat(filepath.Join(creation.Path, name)); !os.IsNotExist(err) {
			t.Fatal("unexpected Git side effect", name, err)
		}
	}
	entries, err := os.ReadDir(external)
	if err != nil || len(entries) != 0 {
		t.Fatal("caller Git target changed", entries, err)
	}
}

func TestRealVirtualEnvironmentUsesFinalProjectPathAndBlocksExistingVenv(t *testing.T) {
	original := os.Getenv("PATH")
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python unavailable")
	}
	service, creation := createdForSetup(t, "python")
	t.Setenv("PATH", original)
	review := requireSetup(t, service, creation, "environment")
	result := service.CommitSetup(context.Background(), review.Token)
	if !result.Succeeded {
		t.Fatal(result)
	}
	python := filepath.Join(creation.Path, ".venv/bin/python")
	output, err := exec.Command(python, "-I", "-B", "-c", "import sys;print(sys.prefix)").CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != filepath.Join(creation.Path, ".venv") {
		t.Fatal("venv kept temporary path", string(output), err)
	}
	config, err := os.ReadFile(filepath.Join(creation.Path, ".venv/pyvenv.cfg"))
	if err != nil || strings.Contains(string(config), "/proc/") {
		t.Fatal("venv is not portable across app restarts", string(config), err)
	}
	if retry := service.ReviewSetup(context.Background(), creation.Token, "environment"); retry.Code != "setup_attempted" {
		t.Fatal(retry)
	}
	if _, err := os.Stat(filepath.Join(creation.Path, "main.py")); err != nil {
		t.Fatal("source removed")
	}
}

func TestSetupShutdownCancelsOwnedWork(t *testing.T) {
	service, creation := createdForSetup(t, "go")
	id := setupLogProject(service, creation)
	entered := make(chan struct{})
	service.setupRunner = func(ctx context.Context, _ domain.CommandAction, _ string, _ *os.File) bootstrap.CommandResult {
		close(entered)
		<-ctx.Done()
		return bootstrap.CommandResult{Started: true, ExitCode: -1, Err: ctx.Err()}
	}
	review := requireSetup(t, service, creation, "git")
	finished := make(chan SetupResult, 1)
	go func() { finished <- service.CommitSetup(context.Background(), review.Token) }()
	<-entered
	if err := service.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-finished:
		if result.Succeeded || result.Code != "cancelled" || !result.LogSaved {
			t.Fatal(result)
		}
	case <-time.After(time.Second):
		t.Fatal("setup did not cancel on shutdown")
	}
	reopened := NewService(nil)
	reopened.paths = service.paths
	defer reopened.ServiceShutdown()
	logs := reopened.ListSetupLogs(context.Background(), id)
	if logs.Code != "" || len(logs.Logs) != 1 || logs.Logs[0].Result == nil || logs.Logs[0].Result.Code != "cancelled" {
		t.Fatal(logs)
	}
}

func TestRealNodeInstallersSkipScriptsAndIsolateAncestorWorkspaces(t *testing.T) {
	original := os.Getenv("PATH")
	for _, manager := range []string{"npm", "pnpm"} {
		t.Run(manager, func(t *testing.T) {
			if _, err := exec.LookPath(manager); err != nil {
				t.Skip(manager + " unavailable")
			}
			service, draft := nodeCreationFixture(t)
			review := creationReview(t, service, draft)
			// A test-owned local dependency avoids registry access while exercising the
			// real installer and both root/dependency lifecycle-script suppression.
			held := service.creations[review.Token]
			held.review.Plan.DependencySetup.Program = manager
			for i, file := range held.review.Plan.Files {
				if file.Path == "package.json" {
					held.review.Plan.Files[i].Contents = `{"name":"installer-fixture","version":"1.0.0","private":true,"scripts":{"preinstall":"node danger.cjs"},"dependencies":{"tiny-fixture":"file:fixtures/tiny"}}`
				}
			}
			held.review.Plan.Files = append(held.review.Plan.Files, bootstrap.File{Path: "danger.cjs", Contents: `require('node:fs').writeFileSync('must-not-run','root hook ran')`}, bootstrap.File{Path: "fixtures/tiny/package.json", Contents: `{"name":"tiny-fixture","version":"1.0.0","main":"index.cjs","scripts":{"install":"node danger.cjs"}}`}, bootstrap.File{Path: "fixtures/tiny/index.cjs", Contents: `module.exports='installed fixture'`}, bootstrap.File{Path: "fixtures/tiny/danger.cjs", Contents: `require('node:fs').writeFileSync('must-not-run','dependency hook ran')`})
			created := service.CommitCreation(context.Background(), review.Token)
			if !created.Registered {
				t.Fatal(created)
			}
			t.Setenv("PATH", original)
			// Parent workspace configuration must never redirect the install or make it
			// install another project; the selected manager is told to ignore workspaces.
			if err := os.WriteFile(filepath.Join(draft.ParentDirectory, "package.json"), []byte(`{"name":"ancestor","workspaces":["*"]}`), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(draft.ParentDirectory, "pnpm-workspace.yaml"), []byte("packages:\n  - '*'\n"), 0600); err != nil {
				t.Fatal(err)
			}
			setup := requireSetup(t, service, review, "dependencies")
			result := service.CommitSetup(context.Background(), setup.Token)
			if !result.Succeeded || !result.Started {
				t.Fatal(result)
			}
			for _, name := range []string{"must-not-run", "fixtures/tiny/must-not-run", "node_modules/tiny-fixture/must-not-run"} {
				if _, err := os.Stat(filepath.Join(review.Path, name)); !os.IsNotExist(err) {
					t.Fatal("installer executed a lifecycle hook", name, err)
				}
			}
			if _, err := os.Stat(filepath.Join(draft.ParentDirectory, "node_modules")); !os.IsNotExist(err) {
				t.Fatal("installed into ancestor workspace", err)
			}
			node, err := exec.LookPath("node")
			if err != nil {
				t.Fatal(err)
			}
			probe := exec.Command(node, "-e", `console.log(require('./node_modules/tiny-fixture'))`)
			probe.Dir = review.Path
			output, err := probe.CombinedOutput()
			if err != nil || strings.TrimSpace(string(output)) != "installed fixture" {
				t.Fatal("installed package depends on removed temporary cache", string(output), err)
			}
		})
	}
}

func TestFastAPIDependencySetupUsesOwnedVenvAndInstallsOffline(t *testing.T) {
	wheels := os.Getenv("WORKSPACE_MANAGER_FASTAPI_WHEELS")
	if wheels == "" {
		t.Skip("set WORKSPACE_MANAGER_FASTAPI_WHEELS for the offline dependency fixture")
	}
	wheels, err := filepath.Abs(wheels)
	if err != nil {
		t.Fatal(err)
	}
	original := os.Getenv("PATH")
	service, creation := createdForSetup(t, "fastapi")
	t.Setenv("PATH", original)
	if review := service.ReviewSetup(context.Background(), creation.Token, "dependencies"); review.Code != "setup_source" {
		t.Fatal("accepted missing venv", review)
	}
	environment := requireSetup(t, service, creation, "environment")
	if result := service.CommitSetup(context.Background(), environment.Token); !result.Succeeded {
		t.Fatal(result)
	}
	dependencies := requireSetup(t, service, creation, "dependencies")
	versions := service.CheckSetupToolVersions(context.Background(), dependencies.Token)
	if versions.Code != "" || len(versions.Checks) != 2 || versions.Checks[0].Status != "supported" || versions.Checks[1].Tool != "pip" || versions.Checks[1].Status != "detected" {
		t.Fatal("owned venv version probes failed", versions)
	}
	if dependencies.Action.Executable != filepath.Join(creation.Path, ".venv/bin/python") {
		t.Fatal("pip lost its venv", dependencies)
	}
	// Only this test adds offline-wheelhouse flags. The production command always
	// shows and uses the public index; tests never install into system Python.
	service.setupRunner = func(ctx context.Context, action domain.CommandAction, node string, root *os.File) bootstrap.CommandResult {
		action.Arguments = append(action.Arguments, "--no-index", "--find-links", wheels)
		return bootstrap.RunSetup(ctx, action, node, root)
	}
	result := service.CommitSetup(context.Background(), dependencies.Token)
	if !result.Succeeded {
		t.Fatal(result)
	}
	probe := exec.Command(dependencies.Action.Executable, "-B", "-m", "unittest", "discover", "-s", "tests", "-v")
	probe.Dir = creation.Path
	output, err := probe.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Ran 3 tests") || !strings.Contains(string(output), "OK") {
		t.Fatal(string(output), err)
	}
	if project := service.ListProjects(context.Background()).State.Projects[0].Project; project.Commands[0].Approval != nil {
		t.Fatal("dependency setup approved startup")
	}
}

func TestSlowSetupKeepsCreationAvailableForTheNextStep(t *testing.T) {
	service, creation := createdForSetup(t, "python")
	setup := requireSetup(t, service, creation, "git")
	service.setupRunner = func(_ context.Context, action domain.CommandAction, _ string, _ *os.File) bootstrap.CommandResult {
		_ = os.Mkdir(filepath.Join(action.WorkingDirectory, ".git"), 0700)
		service.now = func() time.Time { return setup.ExpiresAt.Add(time.Minute) }
		return bootstrap.CommandResult{Started: true, ExitCode: 0}
	}
	if result := service.CommitSetup(context.Background(), setup.Token); !result.Succeeded {
		t.Fatal(result)
	}
	// Opening another creation prunes expired entries. A completed slow setup must
	// keep its originating creation available for the following venv review.
	next := service.ReviewCreation(context.Background(), CreationDraft{TemplateID: "python", Name: "another-python", ParentDirectory: filepath.Dir(creation.Path)})
	if next.Review == nil {
		t.Fatal(next)
	}
	if result := service.ReviewSetup(context.Background(), creation.Token, "environment"); result.Review == nil {
		t.Fatal("slow setup lost its continuation", result)
	}
}
