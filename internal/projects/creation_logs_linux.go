package projects

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/projects/bootstrap"
	"workspace-manager/internal/storage"
)

const creationHistoryPageSize = 20

type CreationHistoryStatus struct {
	ID      domain.ID `json:"id"`
	Saved   bool      `json:"saved"`
	Message string    `json:"message"`
}
type CreationLogsResult struct {
	Logs    []domain.CreationLog `json:"logs"`
	Next    domain.ID            `json:"next"`
	Code    string               `json:"code"`
	Message string               `json:"message"`
}
type creationLogWriter interface {
	LogID() domain.ID
	Finish(context.Context, domain.CreationLogResult, time.Time) error
	Close() error
}
type creationLogSession struct {
	repository *storage.Repository[domain.CreationLog]
	log        domain.CreationLog
	revision   storage.Revision
}

func openCreationLogs(paths storage.Paths) (*storage.Repository[domain.CreationLog], error) {
	if !filepath.IsAbs(paths.StateDir) {
		return nil, storage.ErrInvalidDocument
	}
	codec, err := storage.NewCodec[domain.CreationLog](storage.InitialSchemaVersion, nil)
	if err != nil {
		return nil, err
	}
	return storage.OpenRepository(paths.CreationLogsDir(), codec)
}
func startCreationLog(ctx context.Context, paths storage.Paths, log domain.CreationLog) (creationLogWriter, error) {
	repository, err := openCreationLogs(paths)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			repository.Close()
		}
	}()
	if err := repository.Create(ctx, log); err != nil {
		return nil, err
	}
	snapshot, err := repository.Get(ctx, log.ID)
	if err != nil {
		return nil, err
	}
	keep = true
	return &creationLogSession{repository: repository, log: log, revision: snapshot.Revision}, nil
}
func (session *creationLogSession) LogID() domain.ID { return session.log.ID }
func (session *creationLogSession) Close() error     { return session.repository.Close() }
func (session *creationLogSession) Finish(ctx context.Context, result domain.CreationLogResult, now time.Time) error {
	if now.Before(session.log.RecordedAt) {
		now = session.log.RecordedAt
	}
	return session.repository.Update(ctx, session.log.ID, session.revision, func(log domain.CreationLog) (domain.CreationLog, error) {
		log.FinishedAt = &now
		log.Result = &result
		return log, nil
	})
}
func (service *Service) beginCreationHistory(ctx context.Context, pending *pendingCreation, kind string) (creationLogWriter, error) {
	id, err := domain.NewID()
	if err != nil {
		return nil, err
	}
	log := domain.CreationLog{ID: id, ProjectID: pending.project.ID, Name: pending.project.Name, Path: pending.project.Path, TemplateID: pending.review.Plan.TemplateID, Kind: kind, RecordedAt: service.now().UTC()}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	seed := domain.BootstrapRecovery{ProjectID: log.ProjectID, Path: log.Path, Kind: kind, RecordedAt: log.RecordedAt}
	if kind == "publication" {
		project := cloneProject(pending.project)
		for i := range project.Commands {
			project.Commands[i].Approval = nil
		}
		seed.Project = &project
		seed.Files, err = bootstrap.RecoveryManifest(pending.review.Plan.Files)
		if err != nil {
			return nil, err
		}
	}
	writer, err := service.creationLogStart(ctx, service.resolvedPaths, log)
	if err != nil {
		return nil, err
	}
	seed.ID = writer.LogID()
	lease, err := startRecoveryCheckpoint(ctx, service.resolvedPaths, seed)
	if err != nil {
		writer.Close()
		return nil, err
	}
	return leasedCreationLog{writer, lease}, nil
}
func finishCreationHistory(ctx context.Context, writer creationLogWriter, result domain.CreationLogResult, now time.Time, token string) *CreationHistoryStatus {
	// Scaffolder messages may contain its temporary cwd, which includes a live
	// review token. Persist diagnostics without retaining that capability.
	result.Output = strings.ReplaceAll(result.Output, token, "[review]")
	result.Output, result.Truncated = bootstrap.SafeSetupOutput(result.Output, result.Truncated)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	status := &CreationHistoryStatus{ID: writer.LogID()}
	if err := writer.Finish(ctx, result, now); err != nil {
		status.Message = "The final history result could not be saved durably. Keep this result and inspect the destination; history may show no final outcome."
	} else {
		status.Saved = true
	}
	return status
}
func publicationLogResult(result CreationResult, count int) domain.CreationLogResult {
	return domain.CreationLogResult{Succeeded: result.Created && result.Registered && result.Code == "", Created: result.Created, Registered: result.Registered, ExitCode: -1, FileCount: count, RecoveryPath: result.RecoveryPath, Activity: result.Logs, Code: result.Code, Message: result.Message}
}
func preparationLogResult(result CreationReviewResult, command bootstrap.CommandResult, count int) domain.CreationLogResult {
	message := result.Message
	if result.Review != nil {
		message = fmt.Sprintf("Prepared %d files for review. No destination was created or registered.", count)
	}
	return domain.CreationLogResult{Succeeded: result.Review != nil, ProcessStarted: command.Started, ExitCode: command.ExitCode, FileCount: count, Output: command.Output, Truncated: command.Truncated, Code: result.Code, Message: message}
}

// ListCreationLogs reads a bounded, chronological page, including attempts that
// never reached registration. Scanning retains at most one page plus one record;
// an unreadable/newer record fails the page without misleading partial success.
func (service *Service) ListCreationLogs(ctx context.Context, before domain.ID) CreationLogsResult {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if before != "" {
		if err := before.Validate(); err != nil {
			return creationLogsFailure(err)
		}
	}
	service.mu.Lock()
	if err := service.openRegistry(); err != nil {
		service.mu.Unlock()
		return creationLogsFailure(err)
	}
	paths := service.resolvedPaths
	service.mu.Unlock()
	repository, err := openCreationLogs(paths)
	if err != nil {
		return creationLogsFailure(err)
	}
	defer repository.Close()
	var cursor domain.CreationLog
	if before != "" {
		snapshot, err := repository.Get(ctx, before)
		if err != nil {
			return creationLogsFailure(err)
		}
		cursor = snapshot.Value
	}
	newer := func(a, b domain.CreationLog) bool {
		if a.RecordedAt.Equal(b.RecordedAt) {
			return a.ID > b.ID
		}
		return a.RecordedAt.After(b.RecordedAt)
	}
	logs := []domain.CreationLog{}
	err = repository.Scan(ctx, func(snapshot storage.Snapshot[domain.CreationLog]) error {
		log := snapshot.Value
		if before != "" && !newer(cursor, log) {
			return nil
		}
		logs = append(logs, log)
		sort.Slice(logs, func(i, j int) bool { return newer(logs[i], logs[j]) })
		if len(logs) > creationHistoryPageSize+1 {
			logs = logs[:creationHistoryPageSize+1]
		}
		return nil
	})
	if err != nil {
		return creationLogsFailure(err)
	}
	result := CreationLogsResult{Logs: logs}
	if len(logs) > creationHistoryPageSize {
		result.Logs = logs[:creationHistoryPageSize]
		result.Next = result.Logs[len(result.Logs)-1].ID
	}
	return result
}
func creationLogsFailure(err error) CreationLogsResult {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return CreationLogsResult{Code: "cancelled", Message: "Creation history loading was cancelled or timed out."}
	}
	return CreationLogsResult{Code: "creation_history", Message: "Could not read or save creation history. Check the app state directory's permissions and stored records. Existing logs and project files were kept."}
}
