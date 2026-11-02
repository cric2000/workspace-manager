package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"workspace-manager/internal/domain"
)

func TestRecoveryCheckpointPermitsOnlyAssessmentUpdates(t *testing.T) {
	repo, _ := entityRepository[domain.BootstrapRecovery](t)
	value := domain.BootstrapRecovery{ID: "11111111111111111111111111111111", ProjectID: "22222222222222222222222222222222", Path: "/tmp/owned", Kind: "environment", RecordedAt: time.Now().UTC()}
	ctx := context.Background()
	if err := repo.Create(ctx, value); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.Get(ctx, value.ID)
	if err != nil {
		t.Fatal(err)
	}
	assessment := &domain.RecoveryAssessment{CheckedAt: time.Now().UTC(), Destination: "directory", Registration: "matching", Files: "not_checked", SetupOutput: "present", Message: "Presence does not prove completion."}
	if err := repo.Update(ctx, value.ID, snapshot.Revision, func(next domain.BootstrapRecovery) (domain.BootstrapRecovery, error) {
		next.Assessment = assessment
		next.Path = "/unrelated"
		return next, nil
	}); !errors.Is(err, ErrInvalidDocument) {
		t.Fatal("checkpoint mutation accepted", err)
	}
	if err := repo.Update(ctx, value.ID, snapshot.Revision, func(next domain.BootstrapRecovery) (domain.BootstrapRecovery, error) {
		next.Assessment = assessment
		return next, nil
	}); err != nil {
		t.Fatal(err)
	}
}
