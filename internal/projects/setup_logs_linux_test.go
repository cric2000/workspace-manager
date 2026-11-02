package projects

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/projects/bootstrap"
	"workspace-manager/internal/storage"
)

func setupLogProject(service *Service, review CreationReview) domain.ID {
	return service.creations[review.Token].project.ID
}

func TestSetupLogsPersistAcrossReopenWithoutExecutingOrSavingApprovalTokens(t *testing.T) {
	service, creation := createdForSetup(t, "go")
	id := setupLogProject(service, creation)
	paths := service.resolvedPaths
	calls := 0
	service.setupRunner = func(ctx context.Context, action domain.CommandAction, _ string, _ *os.File) bootstrap.CommandResult {
		calls++
		pending := service.ListSetupLogs(ctx, id)
		if pending.Code != "" || len(pending.Logs) != 1 || pending.Logs[0].Result != nil {
			t.Fatal("attempt was not durable before spawn", pending)
		}
		if err := os.Mkdir(filepath.Join(action.WorkingDirectory, ".git"), 0700); err != nil {
			t.Fatal(err)
		}
		return bootstrap.CommandResult{Started: true, Output: "Initialized empty repository.\n"}
	}
	review := requireSetup(t, service, creation, "git")
	if logs := service.ListSetupLogs(context.Background(), id); logs.Code != "" || len(logs.Logs) != 0 {
		t.Fatal("review wrote logs", logs)
	}
	result := service.CommitSetup(context.Background(), review.Token)
	if !result.Succeeded || !result.LogSaved || result.LogID == "" || result.LogID == review.Token || result.LogMessage != "" {
		t.Fatal(result)
	}
	if replay := service.CommitSetup(context.Background(), review.Token); replay != result || calls != 1 {
		t.Fatal(replay, calls)
	}
	data, err := os.ReadFile(filepath.Join(paths.SetupLogsDir(), string(id), result.LogID+".json"))
	if err != nil || strings.Contains(string(data), review.Token) || strings.Contains(string(data), creation.Token) || strings.Contains(string(data), "executable") || strings.Contains(string(data), "arguments") {
		t.Fatal("saved executable capability or token", err)
	}
	if err := service.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	reopened := NewService(nil)
	reopened.paths = service.paths
	defer reopened.ServiceShutdown()
	logs := reopened.ListSetupLogs(context.Background(), id)
	if logs.Code != "" || len(logs.Logs) != 1 || logs.Logs[0].ID != domain.ID(result.LogID) || logs.Logs[0].Result == nil || logs.Logs[0].Result.Output != result.Output {
		t.Fatal(logs)
	}
	if got := reopened.CommitSetup(context.Background(), review.Token); got.Started || got.Code != "review_changed" {
		t.Fatal("history resumed setup", got)
	}
	logs.Logs[0].Result.Output = "client mutation"
	if again := reopened.ListSetupLogs(context.Background(), id); again.Logs[0].Result.Output == "client mutation" {
		t.Fatal("mutable saved history")
	}
}

func TestFailedCancelledAndUnstartedSetupResultsRemainDurable(t *testing.T) {
	for _, scenario := range []string{"failure", "cancelled", "no-spawn"} {
		t.Run(scenario, func(t *testing.T) {
			service, creation := createdForSetup(t, "go")
			id := setupLogProject(service, creation)
			service.setupRunner = func(context.Context, domain.CommandAction, string, *os.File) bootstrap.CommandResult {
				err := errors.New("private spawn detail")
				started := true
				if scenario == "cancelled" {
					err = context.Canceled
				}
				if scenario == "no-spawn" {
					started = false
				}
				return bootstrap.CommandResult{Started: started, ExitCode: -1, Err: err, Output: "partial output"}
			}
			review := requireSetup(t, service, creation, "git")
			result := service.CommitSetup(context.Background(), review.Token)
			logs := service.ListSetupLogs(context.Background(), id)
			if result.Succeeded || !result.LogSaved || logs.Code != "" || len(logs.Logs) != 1 || logs.Logs[0].Result == nil || logs.Logs[0].Result.Started != result.Started || logs.Logs[0].Result.Output != "partial output" {
				t.Fatal(result, logs)
			}
		})
	}
}

func TestSetupDoesNotStartWhenLogStorageIsUnsafe(t *testing.T) {
	for _, scenario := range []string{"symlink", "file", "corrupt", "future-schema", "project-mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			service, creation := createdForSetup(t, "go")
			id := setupLogProject(service, creation)
			path := filepath.Join(service.resolvedPaths.SetupLogsDir(), string(id))
			review := requireSetup(t, service, creation, "git")
			service.setupRunner = func(context.Context, domain.CommandAction, string, *os.File) bootstrap.CommandResult {
				t.Fatal("setup ran with unsafe history")
				return bootstrap.CommandResult{}
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "symlink":
				if err := os.Symlink(t.TempDir(), path); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			default:
				repo, err := openSetupLogs(service.resolvedPaths, id)
				if err != nil {
					t.Fatal(err)
				}
				entryID, _ := domain.NewID()
				bytes := []byte("{")
				if scenario == "future-schema" {
					bytes = []byte(`{"schemaVersion":99,"kind":"setup-log","data":{}}`)
				}
				if scenario == "project-mismatch" {
					if err := repo.Create(context.Background(), domain.SetupLog{ID: entryID, ProjectID: domain.ID(strings.Repeat("f", 32)), Kind: "git", Label: "Initialize Git", RecordedAt: time.Now().UTC()}); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(filepath.Join(path, string(entryID)+".json"), bytes, 0600); err != nil {
					t.Fatal(err)
				}
				repo.Close()
			}
			result := service.CommitSetup(context.Background(), review.Token)
			if result.Started || result.Code != "setup_history" || !result.Finished {
				t.Fatal(result)
			}
			if _, err := os.Lstat(filepath.Join(creation.Path, ".git")); !os.IsNotExist(err) {
				t.Fatal("partial project created", err)
			}
		})
	}
}

type failingFinalLog struct{ setupLogWriter }

func (log failingFinalLog) Finish(context.Context, SetupResult, time.Time) error {
	return errors.New("simulated disk failure")
}

func TestFinalLogFailurePreservesRealOutcomeAndPendingRecord(t *testing.T) {
	service, creation := createdForSetup(t, "go")
	id := setupLogProject(service, creation)
	service.logStart = func(ctx context.Context, paths storage.Paths, id domain.ID, review SetupReview, now time.Time) (setupLogWriter, error) {
		log, err := startSetupLog(ctx, paths, id, review, now)
		if err != nil {
			return nil, err
		}
		return failingFinalLog{log}, nil
	}
	service.setupRunner = func(_ context.Context, action domain.CommandAction, _ string, _ *os.File) bootstrap.CommandResult {
		_ = os.Mkdir(filepath.Join(action.WorkingDirectory, ".git"), 0700)
		return bootstrap.CommandResult{Started: true, Output: "keep this output"}
	}
	review := requireSetup(t, service, creation, "git")
	result := service.CommitSetup(context.Background(), review.Token)
	if !result.Succeeded || !result.Started || result.LogSaved || result.LogMessage == "" || result.Output != "keep this output" {
		t.Fatal(result)
	}
	if replay := service.GetSetupOutcome(context.Background(), review.Token); replay != result {
		t.Fatal(replay)
	}
	logs := service.ListSetupLogs(context.Background(), id)
	if logs.Code != "" || len(logs.Logs) != 1 || logs.Logs[0].Result != nil {
		t.Fatal(logs)
	}
	if again := service.ReviewSetup(context.Background(), creation.Token, "git"); again.Code != "setup_attempted" {
		t.Fatal("final log failure allowed retry", again)
	}
}

func TestSetupRechecksInputsAfterSavingAttempt(t *testing.T) {
	service, creation := createdForSetup(t, "go")
	id := setupLogProject(service, creation)
	service.logStart = func(ctx context.Context, paths storage.Paths, id domain.ID, review SetupReview, now time.Time) (setupLogWriter, error) {
		log, err := startSetupLog(ctx, paths, id, review, now)
		if err == nil {
			err = os.WriteFile(review.Action.Executable, []byte("#!/bin/sh\nexit 9\n"), 0700)
		}
		return log, err
	}
	service.setupRunner = func(context.Context, domain.CommandAction, string, *os.File) bootstrap.CommandResult {
		t.Fatal("changed tool executed")
		return bootstrap.CommandResult{}
	}
	review := requireSetup(t, service, creation, "git")
	result := service.CommitSetup(context.Background(), review.Token)
	if result.Started || result.Code != "review_changed" || !result.LogSaved {
		t.Fatal(result)
	}
	logs := service.ListSetupLogs(context.Background(), id)
	if logs.Logs[0].Result == nil || logs.Logs[0].Result.Started {
		t.Fatal(logs)
	}
}

func TestIncompleteHistoryIsReadOnlyAfterRestart(t *testing.T) {
	service, creation := createdForSetup(t, "go")
	id := setupLogProject(service, creation)
	review := requireSetup(t, service, creation, "git")
	log, err := startSetupLog(context.Background(), service.resolvedPaths, id, review, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	log.Close()
	service.ServiceShutdown()
	reopened := NewService(nil)
	reopened.paths = service.paths
	defer reopened.ServiceShutdown()
	for i := 0; i < 2; i++ {
		logs := reopened.ListSetupLogs(context.Background(), id)
		if logs.Code != "" || len(logs.Logs) != 1 || logs.Logs[0].Result != nil {
			t.Fatal(logs)
		}
	}
	if _, err := os.Lstat(filepath.Join(creation.Path, ".git")); !os.IsNotExist(err) {
		t.Fatal("history executed setup", err)
	}
}

func TestSavedSetupOutputIsBoundedAndRedacted(t *testing.T) {
	service, creation := createdForSetup(t, "go")
	id := setupLogProject(service, creation)
	t.Setenv("HTTPS_PROXY", "http://alice:private-pass@proxy.invalid:8080")
	service.setupRunner = func(_ context.Context, action domain.CommandAction, _ string, _ *os.File) bootstrap.CommandResult {
		_ = os.Mkdir(filepath.Join(action.WorkingDirectory, ".git"), 0700)
		return bootstrap.CommandResult{Started: true, Output: "\x1bhttp://alice:private-pass@proxy.invalid private-pass\n" + strings.Repeat("三", 30000)}
	}
	review := requireSetup(t, service, creation, "git")
	result := service.CommitSetup(context.Background(), review.Token)
	if !result.LogSaved || !result.Truncated || len(result.Output) > domain.MaxSetupOutputBytes || strings.Contains(result.Output, "private-pass") || strings.ContainsRune(result.Output, '\x1b') {
		t.Fatal("unsafe output", result.LogMessage)
	}
	logs := service.ListSetupLogs(context.Background(), id)
	if logs.Code != "" || logs.Logs[0].Result.Output != result.Output || !logs.Logs[0].Result.Truncated {
		t.Fatal("saved output differs", logs.Code)
	}
}

func TestSetupHistoryCannotTraverseOutsideStateRoot(t *testing.T) {
	service, _ := creationFixture(t)
	for _, id := range []domain.ID{"../projects", "", "/tmp/other"} {
		if result := service.ListSetupLogs(context.Background(), id); result.Code == "" || result.Logs != nil {
			t.Fatal(result)
		}
	}
}
