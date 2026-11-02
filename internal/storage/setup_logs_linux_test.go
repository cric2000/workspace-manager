package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workspace-manager/internal/domain"
)

func TestSetupLogRepositoryAllowsOnlyOneFinalOutcome(t *testing.T) {
	repository, path := entityRepository[domain.SetupLog](t)
	ctx := context.Background()
	now := time.Now().UTC()
	log := domain.SetupLog{ID: domain.ID(strings.Repeat("1", 32)), ProjectID: domain.ID(strings.Repeat("2", 32)), Kind: "git", Label: "Initialize Git", RecordedAt: now}
	if err := repository.Create(ctx, log); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.Get(ctx, log.ID)
	if err != nil || snapshot.Value.Result != nil {
		t.Fatal(snapshot, err)
	}
	finish := func(value domain.SetupLog) (domain.SetupLog, error) {
		value.FinishedAt = &now
		value.Result = &domain.SetupLogResult{Started: true, Succeeded: true, Output: "completed"}
		return value, nil
	}
	if err := repository.Update(ctx, log.ID, snapshot.Revision, func(value domain.SetupLog) (domain.SetupLog, error) {
		value.ProjectID = domain.ID(strings.Repeat("3", 32))
		return finish(value)
	}); !errors.Is(err, ErrInvalidDocument) {
		t.Fatal("identity change accepted", err)
	}
	if err := repository.Update(ctx, log.ID, snapshot.Revision, finish); err != nil {
		t.Fatal(err)
	}
	final, err := repository.Get(ctx, log.ID)
	if err != nil || final.Value.Result == nil || !final.Value.Result.Succeeded {
		t.Fatal(final, err)
	}
	if err := repository.Update(ctx, log.ID, final.Revision, finish); !errors.Is(err, ErrInvalidDocument) {
		t.Fatal("final outcome overwritten", err)
	}
	if info, err := os.Stat(filepath.Join(path, string(log.ID)+".json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenRepository(path, codecFor[domain.SetupLog](t))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	read, err := reopened.Get(ctx, log.ID)
	if err != nil || read.Value.Result.Output != "completed" {
		t.Fatal(read, err)
	}
}

func TestBoundedListFailsWithoutPartialResults(t *testing.T) {
	repository, _ := entityRepository[domain.SetupLog](t)
	for i := 0; i < 3; i++ {
		id, _ := domain.NewID()
		if err := repository.Create(context.Background(), domain.SetupLog{ID: id, ProjectID: domain.ID(strings.Repeat("2", 32)), Kind: "git", Label: "Initialize Git", RecordedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	if entries, err := repository.ListBounded(context.Background(), 2); !errors.Is(err, ErrRecordLimit) || entries != nil {
		t.Fatal(entries, err)
	}
	if entries, err := repository.ListBounded(context.Background(), 3); err != nil || len(entries) != 3 {
		t.Fatal(entries, err)
	}
}
