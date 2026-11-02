package projects

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/projects/bootstrap"
	"workspace-manager/internal/storage"
)

func TestPublicationHistoryPersistsBeforeFilesAndSurvivesRestart(t *testing.T) {
	service, draft := creationFixture(t)
	review := creationReview(t, service, draft)
	service.creationLogStart = func(ctx context.Context, paths storage.Paths, log domain.CreationLog) (creationLogWriter, error) {
		writer, err := startCreationLog(ctx, paths, log)
		if err != nil {
			return nil, err
		}
		repo, err := openCreationLogs(paths)
		if err != nil {
			t.Fatal(err)
		}
		defer repo.Close()
		snapshot, err := repo.Get(ctx, log.ID)
		if err != nil || snapshot.Value.Result != nil {
			t.Fatal(snapshot, err)
		}
		if _, err := os.Lstat(review.Path); !os.IsNotExist(err) {
			t.Fatal("publication preceded history", err)
		}
		return writer, nil
	}
	if logs := service.ListCreationLogs(context.Background(), ""); logs.Code != "" || len(logs.Logs) != 0 {
		t.Fatal("review saved history", logs)
	}
	result := service.CommitCreation(context.Background(), review.Token)
	if !result.Created || !result.Registered || result.History == nil || !result.History.Saved {
		t.Fatal(result)
	}
	id := result.History.ID
	result.History.ID = "client mutation"
	if replay := service.CommitCreation(context.Background(), review.Token); replay.History.ID != id {
		t.Fatal("history status alias", replay)
	}
	data, err := os.ReadFile(filepath.Join(service.resolvedPaths.CreationLogsDir(), string(id)+".json"))
	if err != nil || strings.Contains(string(data), review.Token) || strings.Contains(string(data), "executable") || strings.Contains(string(data), "package main") {
		t.Fatal("history retained approval or source", err)
	}
	service.ServiceShutdown()
	reopened := NewService(nil)
	reopened.paths = service.paths
	defer reopened.ServiceShutdown()
	logs := reopened.ListCreationLogs(context.Background(), "")
	if logs.Code != "" || len(logs.Logs) != 1 || logs.Logs[0].Result == nil || !logs.Logs[0].Result.Registered || logs.Logs[0].ID != id {
		t.Fatal(logs)
	}
	if got := reopened.CommitCreation(context.Background(), review.Token); got.Created || got.Code != "review_changed" {
		t.Fatal("history resumed creation", got)
	}
}

func TestPreparationHistoryCapturesBoundedOutputAndSeparatePublication(t *testing.T) {
	service, draft := nextCreationFixture(t)
	review := creationReview(t, service, draft)
	t.Setenv("HTTPS_PROXY", "http://alice:private-pass@proxy.invalid")
	service.scaffolder = func(_ context.Context, action domain.CommandAction, _ string) ([]bootstrap.File, bootstrap.CommandResult, error) {
		return []bootstrap.File{{Path: "file", Contents: "reviewed bytes"}}, bootstrap.CommandResult{Started: true, Output: action.WorkingDirectory + "\nhttp://alice:private-pass@proxy.invalid private-pass\n" + strings.Repeat("三", 30000)}, nil
	}
	prepared := service.PrepareCreation(context.Background(), review.Token)
	if prepared.Review == nil || prepared.History == nil || !prepared.History.Saved {
		t.Fatal(prepared)
	}
	logs := service.ListCreationLogs(context.Background(), "")
	if logs.Code != "" || len(logs.Logs) != 1 {
		t.Fatal(logs)
	}
	log := logs.Logs[0]
	if log.Kind != "preparation" || log.Result == nil || !log.Result.Succeeded || log.Result.Created || !log.Result.Truncated || len(log.Result.Output) > domain.MaxSetupOutputBytes || strings.Contains(log.Result.Output, review.Token) || strings.Contains(log.Result.Output, "private-pass") {
		t.Fatal(log.Kind, log.Result)
	}
	prepared.History.Saved = false
	if replay := service.GetPreparationOutcome(context.Background(), review.Token); !replay.History.Saved {
		t.Fatal("mutable history status")
	}
	result := service.CommitCreation(context.Background(), review.Token)
	if !result.Created || !result.History.Saved {
		t.Fatal(result)
	}
	if after := service.ListCreationLogs(context.Background(), ""); len(after.Logs) != 2 {
		t.Fatal(after)
	}
}

func TestCreationHistoryPreservesFailuresAndIncompleteRecords(t *testing.T) {
	for _, scenario := range []string{"conflict", "preparation-failure", "preparation-cancelled", "incomplete"} {
		t.Run(scenario, func(t *testing.T) {
			service, draft := nextCreationFixture(t)
			review := creationReview(t, service, draft)
			service.scaffolder = func(context.Context, domain.CommandAction, string) ([]bootstrap.File, bootstrap.CommandResult, error) {
				err := bootstrap.ErrScaffold
				if scenario == "preparation-cancelled" {
					err = context.Canceled
				}
				return nil, bootstrap.CommandResult{Started: true, ExitCode: -1, Output: "failure output"}, err
			}
			switch scenario {
			case "conflict":
				service.creations[review.Token].review.Prepared = true
				service.creations[review.Token].review.Plan.Files = []bootstrap.File{{Path: "file", Contents: "reviewed"}}
				if err := os.Mkdir(review.Path, 0700); err != nil {
					t.Fatal(err)
				}
				result := service.CommitCreation(context.Background(), review.Token)
				if result.Created || result.Code != "destination_exists" || !result.History.Saved {
					t.Fatal(result)
				}
			case "incomplete":
				writer, err := service.beginCreationHistory(context.Background(), service.creations[review.Token], "preparation")
				if err != nil {
					t.Fatal(err)
				}
				writer.Close()
			default:
				result := service.PrepareCreation(context.Background(), review.Token)
				if result.Review != nil || result.Code == "" || !result.History.Saved {
					t.Fatal(result)
				}
			}
			service.ServiceShutdown()
			reopened := NewService(nil)
			reopened.paths = service.paths
			defer reopened.ServiceShutdown()
			logs := reopened.ListCreationLogs(context.Background(), "")
			if logs.Code != "" || len(logs.Logs) != 1 {
				t.Fatal(logs)
			}
			if scenario == "incomplete" {
				if logs.Logs[0].Result != nil {
					t.Fatal(logs)
				}
			} else if logs.Logs[0].Result == nil || logs.Logs[0].Result.Succeeded {
				t.Fatal(logs)
			}
		})
	}
}

type failedCreationFinal struct{ creationLogWriter }

func (log failedCreationFinal) Finish(context.Context, domain.CreationLogResult, time.Time) error {
	return errors.New("simulated disk failure")
}

func TestCreationHistoryFailureBoundaries(t *testing.T) {
	for _, stage := range []string{"preparation", "publication"} {
		for _, boundary := range []string{"before", "after"} {
			t.Run(stage+"/"+boundary, func(t *testing.T) {
				service, draft := nextCreationFixture(t)
				review := creationReview(t, service, draft)
				calls := 0
				service.scaffolder = func(context.Context, domain.CommandAction, string) ([]bootstrap.File, bootstrap.CommandResult, error) {
					calls++
					return []bootstrap.File{{Path: "file", Contents: "content"}}, bootstrap.CommandResult{Started: true}, nil
				}
				service.creationLogStart = func(ctx context.Context, paths storage.Paths, log domain.CreationLog) (creationLogWriter, error) {
					if boundary == "before" {
						return nil, errors.New("storage failure")
					}
					writer, err := startCreationLog(ctx, paths, log)
					if err != nil {
						return nil, err
					}
					return failedCreationFinal{writer}, nil
				}
				if stage == "preparation" {
					result := service.PrepareCreation(context.Background(), review.Token)
					if boundary == "before" {
						if result.Code != "creation_history" || calls != 0 {
							t.Fatal(result, calls)
						}
					} else if result.Review == nil || result.History.Saved || result.History.Message == "" || calls != 1 {
						t.Fatal(result, calls)
					}
				} else {
					pending := service.creations[review.Token]
					pending.review.Prepared = true
					pending.review.Plan.Files = []bootstrap.File{{Path: "file", Contents: "content"}}
					result := service.CommitCreation(context.Background(), review.Token)
					if boundary == "before" {
						if result.Created || result.Code != "creation_history" {
							t.Fatal(result)
						}
					} else if !result.Created || !result.Registered || result.History.Saved || result.History.Message == "" {
						t.Fatal(result)
					}
				}
			})
		}
	}
}

func TestCreationHistoryPaginatesChronologicallyAndPreservesBadRecords(t *testing.T) {
	service, _ := creationFixture(t)
	service.ListProjects(context.Background())
	repo, err := openCreationLogs(service.resolvedPaths)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	now := time.Now().UTC()
	ids := []domain.ID{}
	for i := 0; i < creationHistoryPageSize+3; i++ {
		id, _ := domain.NewID()
		ids = append(ids, id)
		if err := repo.Create(context.Background(), domain.CreationLog{ID: id, ProjectID: id, Name: "fixture", Path: "/tmp/fixture", TemplateID: "go", Kind: "publication", RecordedAt: now.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	first := service.ListCreationLogs(context.Background(), "")
	second := service.ListCreationLogs(context.Background(), first.Next)
	if first.Code != "" || second.Code != "" || len(first.Logs) != creationHistoryPageSize || len(second.Logs) != 3 || second.Next != "" || first.Logs[0].ID != ids[len(ids)-1] {
		t.Fatal(first.Code, second.Code, len(first.Logs), len(second.Logs))
	}
	seen := map[domain.ID]bool{}
	for _, log := range append(first.Logs, second.Logs...) {
		if seen[log.ID] {
			t.Fatal("duplicate page record")
		}
		seen[log.ID] = true
	}
	if result := service.ListCreationLogs(context.Background(), "../bad"); result.Code == "" || result.Logs != nil {
		t.Fatal(result)
	}
	bad := filepath.Join(service.resolvedPaths.CreationLogsDir(), string(ids[0])+".json")
	data := []byte(`{"schemaVersion":99,"kind":"creation-log","data":{}}`)
	if err := os.WriteFile(bad, data, 0600); err != nil {
		t.Fatal(err)
	}
	if result := service.ListCreationLogs(context.Background(), ""); result.Code == "" || result.Logs != nil {
		t.Fatal("partial history hid unreadable record")
	}
	if after, _ := os.ReadFile(bad); !reflect.DeepEqual(data, after) {
		t.Fatal("history rewrote future schema")
	}
}

func TestCreationHistoryRechecksToolsAfterInitialSave(t *testing.T) {
	service, draft := nextCreationFixture(t)
	review := creationReview(t, service, draft)
	service.creationLogStart = func(ctx context.Context, paths storage.Paths, log domain.CreationLog) (creationLogWriter, error) {
		writer, err := startCreationLog(ctx, paths, log)
		if err == nil {
			if err := os.WriteFile(review.Scaffold.Executable, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
				t.Fatal(err)
			}
		}
		return writer, err
	}
	service.scaffolder = func(context.Context, domain.CommandAction, string) ([]bootstrap.File, bootstrap.CommandResult, error) {
		t.Fatal("changed executable ran")
		return nil, bootstrap.CommandResult{}, nil
	}
	result := service.PrepareCreation(context.Background(), review.Token)
	if result.Code != "review_changed" || result.History == nil || !result.History.Saved {
		t.Fatal(result)
	}
}
