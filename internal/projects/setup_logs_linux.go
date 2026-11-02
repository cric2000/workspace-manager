package projects

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

const maxProjectSetupLogs = 32

type SetupLogsResult struct {
	Logs    []domain.SetupLog `json:"logs"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
}

type setupLogWriter interface {
	LogID() domain.ID
	Finish(context.Context, SetupResult, time.Time) error
	Close() error
}

type setupLogSession struct {
	repository *storage.Repository[domain.SetupLog]
	id         domain.ID
	revision   storage.Revision
	recordedAt time.Time
}

func openSetupLogs(paths storage.Paths, projectID domain.ID) (*storage.Repository[domain.SetupLog], error) {
	if err := projectID.Validate(); err != nil {
		return nil, err
	}
	// An empty state root must never turn persistence into a relative path.
	if !filepath.IsAbs(paths.StateDir) {
		return nil, storage.ErrInvalidDocument
	}
	codec, err := storage.NewCodec[domain.SetupLog](storage.InitialSchemaVersion, nil)
	if err != nil {
		return nil, err
	}
	return storage.OpenRepository(filepath.Join(paths.SetupLogsDir(), string(projectID)), codec)
}

func startSetupLog(ctx context.Context, paths storage.Paths, projectID domain.ID, review SetupReview, now time.Time) (setupLogWriter, error) {
	repository, err := openSetupLogs(paths, projectID)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			repository.Close()
		}
	}()
	logs, err := repository.ListBounded(ctx, maxProjectSetupLogs)
	if err != nil {
		return nil, err
	}
	if len(logs) >= maxProjectSetupLogs {
		return nil, storage.ErrRecordLimit
	}
	for _, log := range logs {
		if log.Value.ProjectID != projectID {
			return nil, storage.ErrInvalidDocument
		}
	}
	id, err := domain.NewID()
	if err != nil {
		return nil, err
	}
	log := domain.SetupLog{ID: id, ProjectID: projectID, Kind: review.Kind, Label: review.Label, RecordedAt: now}
	if err := repository.Create(ctx, log); err != nil {
		return nil, err
	}
	snapshot, err := repository.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	keep = true
	return &setupLogSession{repository: repository, id: id, revision: snapshot.Revision, recordedAt: now}, nil
}

func (session *setupLogSession) LogID() domain.ID { return session.id }
func (session *setupLogSession) Close() error     { return session.repository.Close() }
func (session *setupLogSession) Finish(ctx context.Context, result SetupResult, finishedAt time.Time) error {
	// Wall-clock adjustments must not make an otherwise valid result unsavable.
	if finishedAt.Before(session.recordedAt) {
		finishedAt = session.recordedAt
	}
	return session.repository.Update(ctx, session.id, session.revision, func(log domain.SetupLog) (domain.SetupLog, error) {
		log.FinishedAt = &finishedAt
		log.Result = &domain.SetupLogResult{Started: result.Started, Succeeded: result.Succeeded, ExitCode: result.ExitCode, Output: result.Output, Truncated: result.Truncated, Code: result.Code, Message: result.Message}
		return log, nil
	})
}

func finishSetupLog(ctx context.Context, log setupLogWriter, result *SetupResult, now time.Time) {
	result.LogID = string(log.LogID())
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := log.Finish(finishCtx, *result, now); err != nil {
		result.LogMessage = "The final result could not be saved durably. Keep this output and inspect project files; setup will not retry automatically. History may show no final outcome."
	} else {
		result.LogSaved = true
	}
}

// ListSetupLogs reads diagnostic records only. Missing final outcomes are never
// interpreted as live processes or executable retry/approval instructions.
func (service *Service) ListSetupLogs(ctx context.Context, projectID domain.ID) SetupLogsResult {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	service.mu.Lock()
	if err := projectID.Validate(); err != nil {
		service.mu.Unlock()
		return setupLogsFailure(err)
	}
	if err := service.openRegistry(); err != nil {
		service.mu.Unlock()
		return setupLogsFailure(err)
	}
	paths := service.resolvedPaths
	service.mu.Unlock()
	repository, err := openSetupLogs(paths, projectID)
	if err != nil {
		return setupLogsFailure(err)
	}
	defer repository.Close()
	snapshots, err := repository.ListBounded(ctx, maxProjectSetupLogs)
	if err != nil {
		return setupLogsFailure(err)
	}
	logs := make([]domain.SetupLog, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot.Value.ProjectID != projectID {
			return setupLogsFailure(storage.ErrInvalidDocument)
		}
		logs = append(logs, snapshot.Value)
	}
	sort.Slice(logs, func(i, j int) bool {
		if logs[i].RecordedAt.Equal(logs[j].RecordedAt) {
			return logs[i].ID > logs[j].ID
		}
		return logs[i].RecordedAt.After(logs[j].RecordedAt)
	})
	return SetupLogsResult{Logs: logs}
}

func setupLogsFailure(err error) SetupLogsResult {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return SetupLogsResult{Code: "cancelled", Message: "Setup history loading was cancelled or timed out."}
	}
	if errors.Is(err, storage.ErrRecordLimit) {
		return SetupLogsResult{Code: "history_limit", Message: "Setup history exceeds the 32-record project limit. Existing logs were kept."}
	}
	return SetupLogsResult{Code: "setup_history", Message: "Could not read or save setup history. Check the app state directory's permissions and stored records. Existing logs and project files were kept."}
}
