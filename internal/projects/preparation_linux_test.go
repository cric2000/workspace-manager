package projects

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/projects/bootstrap"
)

func nextCreationFixture(t *testing.T) (*Service, CreationDraft) {
	t.Helper()
	service, draft := creationFixture(t)
	draft.TemplateID, draft.Name = "nextjs", "hello-next"
	draft.ModulePath = ""
	for _, program := range []string{"node", "npm"} {
		if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), program), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return service, draft
}
func TestNextPreparationAndPublicationAreSeparateHeldApprovals(t *testing.T) {
	service, draft := nextCreationFixture(t)
	ctx := context.Background()
	calls := 0
	service.scaffolder = func(_ context.Context, action domain.CommandAction, name string) ([]bootstrap.File, bootstrap.CommandResult, error) {
		calls++
		if name != draft.Name || !strings.Contains(strings.Join(action.Arguments, " "), "--skip-install") || !strings.HasPrefix(action.WorkingDirectory, os.TempDir()+"/workspace-manager-next-") {
			t.Fatal(action, name)
		}
		return []bootstrap.File{{Path: "package.json", Contents: "reviewed bytes"}, {Path: "src/app/page.tsx", Contents: "generated"}}, bootstrap.CommandResult{Started: true}, nil
	}
	review := creationReview(t, service, draft)
	if review.Scaffold == nil || filepath.Base(review.Scaffold.Executable) != "node" || len(review.Scaffold.Arguments) == 0 || filepath.Base(review.Scaffold.Arguments[0]) != "npm" {
		t.Fatal("scaffolder interpreter was not pinned", review)
	}
	if calls != 0 || review.Prepared || review.Scaffold == nil || len(review.Plan.Files) != 0 {
		t.Fatal(review, calls)
	}
	if got := service.GetPreparationOutcome(ctx, review.Token); got.Code != "preparation_pending" || calls != 0 {
		t.Fatal(got, calls)
	}
	if got := service.CommitCreation(ctx, review.Token); got.Created || got.Code != "preparation_required" {
		t.Fatal(got)
	}
	review.Scaffold.Arguments[0] = "client mutation"
	review.Plan.Scaffold.Arguments[0] = "client mutation"
	prepared := service.PrepareCreation(ctx, review.Token)
	if prepared.Review == nil || !prepared.Review.Prepared || len(prepared.Review.Plan.Files) != 2 || calls != 1 {
		t.Fatal(prepared, calls)
	}
	if _, err := os.Stat(review.Path); !os.IsNotExist(err) {
		t.Fatal("preparation published", err)
	}
	if projects := service.ListProjects(ctx); projects.Code != "" || len(projects.State.Projects) != 0 {
		t.Fatal(projects)
	}
	prepared.Review.Plan.Files[0].Contents = "client mutation"
	checked := service.GetPreparationOutcome(ctx, review.Token)
	if checked.Review.Plan.Files[0].Contents != "reviewed bytes" {
		t.Fatal("held output aliases response")
	}
	if again := service.PrepareCreation(ctx, review.Token); !reflect.DeepEqual(again, checked) || calls != 1 {
		t.Fatal("preparation replay executed")
	}
	created := service.CommitCreation(ctx, review.Token)
	if created.Code != "" || !created.Created || !created.Registered {
		t.Fatal(created)
	}
	data, _ := os.ReadFile(filepath.Join(review.Path, "package.json"))
	if string(data) != "reviewed bytes" {
		t.Fatal("unreviewed output published")
	}
	project := created.State.Projects[0].Project
	if project.Source != domain.SourceBootstrapped || project.Commands[0].Approval != nil || project.Preferences.PreviewURL != "http://127.0.0.1:3000" {
		t.Fatal(project)
	}
	if calls != 1 {
		t.Fatal("publication reran scaffolder")
	}
}

func TestNextPreparationCancellationIsReadOnlyQueryableAndNeverRetried(t *testing.T) {
	service, draft := nextCreationFixture(t)
	entered, stopped := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	service.scaffolder = func(ctx context.Context, _ domain.CommandAction, _ string) ([]bootstrap.File, bootstrap.CommandResult, error) {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		close(stopped)
		return nil, bootstrap.CommandResult{Started: true, ExitCode: -1}, ctx.Err()
	}
	review := creationReview(t, service, draft)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan CreationReviewResult, 1)
	go func() { finished <- service.PrepareCreation(ctx, review.Token) }()
	<-entered
	if got := service.PrepareCreation(context.Background(), review.Token); got.Code != "preparation_pending" {
		t.Fatal(got)
	}
	if got := service.GetPreparationOutcome(context.Background(), review.Token); got.Code != "preparation_pending" || calls.Load() != 1 {
		t.Fatal(got)
	}
	// Slow subprocesses never hold the service mutex or the registry's write lock.
	listed := make(chan RegistryResult, 1)
	go func() { listed <- service.ListProjects(context.Background()) }()
	select {
	case result := <-listed:
		if result.Code != "" {
			t.Fatal(result)
		}
	case <-time.After(time.Second):
		t.Fatal("scaffolder blocked registry")
	}
	cancel()
	<-stopped
	if got := <-finished; got.Code != "cancelled" {
		t.Fatal(got)
	}
	if got := service.GetPreparationOutcome(context.Background(), review.Token); got.Code != "cancelled" {
		t.Fatal(got)
	}
	if got := service.PrepareCreation(context.Background(), review.Token); got.Code != "cancelled" || calls.Load() != 1 {
		t.Fatal("failed operation retried", got)
	}
	if _, err := os.Stat(review.Path); !os.IsNotExist(err) {
		t.Fatal("cancelled preparation published")
	}
}

func TestNextPreparationRejectsChangedPrerequisitesAndPreservesDestination(t *testing.T) {
	for _, scenario := range []string{"tool", "parent", "destination", "failure", "publication-conflict", "shutdown"} {
		t.Run(scenario, func(t *testing.T) {
			service, draft := nextCreationFixture(t)
			calls := 0
			service.scaffolder = func(_ context.Context, _ domain.CommandAction, _ string) ([]bootstrap.File, bootstrap.CommandResult, error) {
				calls++
				if scenario == "failure" {
					return nil, bootstrap.CommandResult{Started: true, ExitCode: 1}, bootstrap.ErrScaffold
				}
				return []bootstrap.File{{Path: "file", Contents: "generated"}}, bootstrap.CommandResult{Started: true}, nil
			}
			review := creationReview(t, service, draft)
			switch scenario {
			case "tool":
				if err := os.WriteFile(review.Scaffold.Executable, []byte("changed"), 0700); err != nil {
					t.Fatal(err)
				}
			case "parent":
				if err := os.Rename(draft.ParentDirectory, draft.ParentDirectory+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(draft.ParentDirectory, 0700); err != nil {
					t.Fatal(err)
				}
			case "destination":
				if err := os.Mkdir(review.Path, 0700); err != nil {
					t.Fatal(err)
				}
			case "shutdown":
				_ = service.ServiceShutdown()
			}
			result := service.PrepareCreation(context.Background(), review.Token)
			if scenario == "publication-conflict" {
				if result.Review == nil {
					t.Fatal(result)
				}
				if err := os.Mkdir(review.Path, 0700); err != nil {
					t.Fatal(err)
				}
				file := filepath.Join(review.Path, "user.txt")
				if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				created := service.CommitCreation(context.Background(), review.Token)
				if created.Created || created.Code != "destination_exists" {
					t.Fatal(created)
				}
				data, _ := os.ReadFile(file)
				if string(data) != "keep" {
					t.Fatal("overwrote destination")
				}
			} else if result.Code == "" || result.Review != nil {
				t.Fatal(result)
			}
			if scenario != "failure" && scenario != "publication-conflict" && calls != 0 {
				t.Fatal("prerequisite change executed tool")
			}
		})
	}
}

func TestShutdownCancelsRunningPreparation(t *testing.T) {
	service, draft := nextCreationFixture(t)
	entered := make(chan struct{})
	service.scaffolder = func(ctx context.Context, _ domain.CommandAction, _ string) ([]bootstrap.File, bootstrap.CommandResult, error) {
		close(entered)
		<-ctx.Done()
		return nil, bootstrap.CommandResult{Started: true, ExitCode: -1}, errors.New("cancelled")
	}
	review := creationReview(t, service, draft)
	finished := make(chan CreationReviewResult, 1)
	go func() { finished <- service.PrepareCreation(context.Background(), review.Token) }()
	<-entered
	if err := service.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-finished:
		if got.Code != "closed" || got.History == nil || !got.History.Saved {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown left preparation running")
	}
}
