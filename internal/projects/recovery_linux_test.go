package projects

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"workspace-manager/internal/domain"
	"workspace-manager/internal/projects/bootstrap"
	"workspace-manager/internal/storage"
)

func recoverableFixture(t *testing.T) (*Service, RecoveryTarget, CreationReview) {
	t.Helper()
	service, draft := creationFixture(t)
	review := creationReview(t, service, draft)
	pending := service.creations[review.Token]
	writer, err := service.beginCreationHistory(context.Background(), pending, "publication")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := bootstrap.OpenParent(draft.ParentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := parent.Publish(context.Background(), draft.Name, review.Plan.Files)
	parent.Close()
	if err != nil || !publication.Durable {
		t.Fatal(publication, err)
	}
	target := RecoveryTarget{Scope: "creation", ProjectID: pending.project.ID, LogID: writer.LogID()}
	writer.Close()
	return service, target, review
}

func TestRestartRecoveryRequiresFreshReviewAndRestoresOnlyUnapprovedRegistration(t *testing.T) {
	service, target, creation := recoverableFixture(t)
	service.ServiceShutdown()
	reopened := NewService(nil)
	reopened.paths = service.paths
	defer reopened.ServiceShutdown()
	check := reopened.CheckBootstrapRecovery(context.Background(), target)
	if check.Code != "" || check.Report == nil || check.Report.Owner != "released" || !check.Report.CanRestoreRegistration || !check.Report.Saved || check.Report.Assessment.Files != "matching" {
		t.Fatal(check)
	}
	if projects := reopened.ListProjects(context.Background()); len(projects.State.Projects) != 0 {
		t.Fatal("checking recovery registered project")
	}
	review := reopened.ReviewRecoveryRegistration(context.Background(), target)
	if review.Code != "" || review.Review == nil || review.Review.Project.ID != target.ProjectID || review.Review.Project.Commands[0].Approval != nil {
		t.Fatal(review)
	}
	if outcome := reopened.GetRecoveryRegistrationOutcome(context.Background(), review.Review.Token); outcome.Code != "recovery_pending" {
		t.Fatal(outcome)
	}
	review.Review.Project.Commands[0].Configuration.Arguments[0] = "client mutation"
	result := reopened.CommitRecoveryRegistration(context.Background(), review.Review.Token)
	if !result.Committed || result.Code != "" || result.State == nil {
		t.Fatal(result)
	}
	project := result.State.Projects[0].Project
	if project.ID != target.ProjectID || project.Source != domain.SourceBootstrapped || project.Commands[0].Approval != nil || project.Commands[0].Configuration.Arguments[0] != "run" {
		t.Fatal(project)
	}
	if replay := reopened.CommitRecoveryRegistration(context.Background(), review.Review.Token); !reflect.DeepEqual(result, replay) {
		t.Fatal(replay)
	}
	for _, file := range creation.Plan.Files {
		data, err := os.ReadFile(filepath.Join(creation.Path, file.Path))
		if err != nil || string(data) != file.Contents {
			t.Fatal("recovery changed source", file.Path, err)
		}
	}
	if again := reopened.CheckBootstrapRecovery(context.Background(), target); again.Report.Assessment.Registration != "matching" || again.Report.CanRestoreRegistration {
		t.Fatal(again)
	}
	if got := reopened.CommitCreation(context.Background(), creation.Token); got.Created {
		t.Fatal("recovery reused old creation capability")
	}
}

func TestRecoveryRejectsChangedUnsafeAndUntrackedEvidence(t *testing.T) {
	for _, scenario := range []string{"changed-file", "extra-file", "symlink", "fifo", "legacy", "conflict", "bad-checkpoint"} {
		t.Run(scenario, func(t *testing.T) {
			service, target, creation := recoverableFixture(t)
			switch scenario {
			case "changed-file":
				os.WriteFile(filepath.Join(creation.Path, "main.go"), []byte("edited"), 0600)
			case "extra-file":
				os.WriteFile(filepath.Join(creation.Path, ".npmrc"), []byte("preserve"), 0600)
			case "symlink":
				os.Remove(filepath.Join(creation.Path, "main.go"))
				os.Symlink(filepath.Join(creation.Path, "go.mod"), filepath.Join(creation.Path, "main.go"))
			case "fifo":
				os.Remove(filepath.Join(creation.Path, "main.go"))
				if err := unix.Mkfifo(filepath.Join(creation.Path, "main.go"), 0600); err != nil {
					t.Fatal(err)
				}
			case "legacy":
				os.Remove(filepath.Join(service.resolvedPaths.BootstrapRecoveryDir(), "records", string(target.LogID)+".json"))
			case "bad-checkpoint":
				os.WriteFile(filepath.Join(service.resolvedPaths.BootstrapRecoveryDir(), "records", string(target.LogID)+".json"), []byte(`{"schemaVersion":99,"kind":"bootstrap-recovery","data":{}}`), 0600)
			case "conflict":
				project := cloneProject(service.creations[creation.Token].project)
				project.ID, _ = domain.NewID()
				if err := service.registry.Create(context.Background(), project); err != nil {
					t.Fatal(err)
				}
			}
			result := service.CheckBootstrapRecovery(context.Background(), target)
			if result.Report != nil && result.Report.CanRestoreRegistration {
				t.Fatal("unsafe recovery allowed", result)
			}
			if review := service.ReviewRecoveryRegistration(context.Background(), target); review.Review != nil || review.Code == "" {
				t.Fatal(review)
			}
			if scenario == "legacy" && (result.Report == nil || result.Report.Owner != "untracked") {
				t.Fatal(result)
			}
		})
	}
}

func TestRecoveryCommitRechecksTreeRootAndExpiry(t *testing.T) {
	for _, scenario := range []string{"changed", "root", "expired", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			service, target, creation := recoverableFixture(t)
			review := service.ReviewRecoveryRegistration(context.Background(), target)
			if review.Review == nil {
				t.Fatal(review)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "changed":
				os.WriteFile(filepath.Join(creation.Path, "main.go"), []byte("edit"), 0600)
			case "root":
				os.Rename(creation.Path, creation.Path+"-owned")
				os.Mkdir(creation.Path, 0700)
				for _, file := range creation.Plan.Files {
					os.WriteFile(filepath.Join(creation.Path, file.Path), []byte(file.Contents), 0600)
				}
			case "expired":
				service.now = func() time.Time { return review.Review.ExpiresAt.Add(time.Second) }
			case "cancelled":
				cancel()
			}
			if result := service.CommitRecoveryRegistration(ctx, review.Review.Token); result.Committed || result.Code == "" {
				t.Fatal(result)
			}
			if projects := service.ListProjects(context.Background()); len(projects.State.Projects) != 0 {
				t.Fatal("unsafe registry write")
			}
		})
	}
}

// The helper exits only when killed by its owning test. No project code runs.
func TestBootstrapRecoveryCrashHelper(t *testing.T) {
	if os.Getenv("WORKSPACE_MANAGER_RECOVERY_HELPER") != "1" {
		return
	}
	var paths storage.Paths
	if err := json.Unmarshal([]byte(os.Getenv("WORKSPACE_MANAGER_RECOVERY_PATHS")), &paths); err != nil {
		t.Fatal(err)
	}
	service := NewService(nil)
	service.paths = func() (storage.Paths, error) { return paths, nil }
	draft := CreationDraft{TemplateID: "go", Name: "crash-project", ParentDirectory: os.Getenv("WORKSPACE_MANAGER_RECOVERY_PARENT"), ModulePath: "example.com/crash"}
	review := creationReview(t, service, draft)
	pending := service.creations[review.Token]
	writer, err := service.beginCreationHistory(context.Background(), pending, "publication")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := bootstrap.OpenParent(draft.ParentDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := parent.Publish(context.Background(), draft.Name, review.Plan.Files); err != nil || !result.Published {
		t.Fatal(result, err)
	}
	parent.Close()
	data, _ := json.Marshal(RecoveryTarget{Scope: "creation", ProjectID: pending.project.ID, LogID: writer.LogID()})
	if err := os.WriteFile(os.Getenv("WORKSPACE_MANAGER_RECOVERY_READY"), data, 0600); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestRecoveryDetectsLiveOwnerThenRealProcessDeath(t *testing.T) {
	service, draft := creationFixture(t)
	service.ListProjects(context.Background())
	paths, _ := json.Marshal(service.resolvedPaths)
	ready := filepath.Join(t.TempDir(), "ready.json")
	command := exec.Command(os.Args[0], "-test.run=^TestBootstrapRecoveryCrashHelper$")
	command.Env = append(os.Environ(), "WORKSPACE_MANAGER_RECOVERY_HELPER=1", "WORKSPACE_MANAGER_RECOVERY_PATHS="+string(paths), "WORKSPACE_MANAGER_RECOVERY_PARENT="+draft.ParentDirectory, "WORKSPACE_MANAGER_RECOVERY_READY="+ready)
	var output strings.Builder
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			command.Process.Kill()
			command.Wait()
		}
	}()
	var target RecoveryTarget
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(ready)
		if json.Unmarshal(data, &target) == nil && target.LogID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if target.LogID == "" {
		command.Process.Kill()
		command.Wait()
		waited = true
		t.Fatal("helper did not start", output.String())
	}
	if check := service.CheckBootstrapRecovery(context.Background(), target); check.Code != "" || check.Report == nil || check.Report.Owner != "active" {
		t.Fatal(check)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	command.Wait()
	waited = true
	check := service.CheckBootstrapRecovery(context.Background(), target)
	if check.Code != "" || check.Report == nil || check.Report.Owner != "released" || !check.Report.CanRestoreRegistration {
		t.Fatal(check)
	}
	review := service.ReviewRecoveryRegistration(context.Background(), target)
	if review.Review == nil {
		t.Fatal(review)
	}
	if result := service.CommitRecoveryRegistration(context.Background(), review.Review.Token); !result.Committed {
		t.Fatal(result)
	}
}

func TestSetupRecoveryPreservesPartialOutputAndDoesNotInferCompletion(t *testing.T) {
	service, creation := createdForSetup(t, "go")
	review := requireSetup(t, service, creation, "git")
	entered, finished := make(chan struct{}), make(chan SetupResult, 1)
	service.setupRunner = func(ctx context.Context, action domain.CommandAction, _ string, _ *os.File) bootstrap.CommandResult {
		if err := os.Mkdir(filepath.Join(action.WorkingDirectory, ".git"), 0700); err != nil {
			t.Fatal(err)
		}
		close(entered)
		<-ctx.Done()
		return bootstrap.CommandResult{Started: true, ExitCode: -1, Err: ctx.Err()}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { finished <- service.CommitSetup(ctx, review.Token) }()
	<-entered
	logs := service.ListSetupLogs(context.Background(), setupLogProject(service, creation))
	target := RecoveryTarget{Scope: "setup", ProjectID: logs.Logs[0].ProjectID, LogID: logs.Logs[0].ID}
	if check := service.CheckBootstrapRecovery(context.Background(), target); check.Report == nil || check.Report.Owner != "active" {
		t.Fatal(check)
	}
	cancel()
	<-finished
	service.ServiceShutdown()
	reopened := NewService(nil)
	reopened.paths = service.paths
	defer reopened.ServiceShutdown()
	check := reopened.CheckBootstrapRecovery(context.Background(), target)
	if check.Report == nil || check.Report.Owner != "released" || check.Report.Assessment.SetupOutput != "present" || check.Report.CanRestoreRegistration || !strings.Contains(check.Report.Message, "do not prove setup completed") {
		t.Fatal(check)
	}
	if _, err := os.Stat(filepath.Join(creation.Path, ".git")); err != nil {
		t.Fatal("recovery removed partial output", err)
	}
}
