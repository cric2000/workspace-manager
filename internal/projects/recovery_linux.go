package projects

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
	"workspace-manager/internal/domain"
	"workspace-manager/internal/projects/bootstrap"
	"workspace-manager/internal/storage"
)

type RecoveryTarget struct {
	Scope     string    `json:"scope"`
	ProjectID domain.ID `json:"projectId"`
	LogID     domain.ID `json:"logId"`
}
type BootstrapRecoveryReport struct {
	Owner                  string                     `json:"owner"`
	Path                   string                     `json:"path"`
	Assessment             *domain.RecoveryAssessment `json:"assessment,omitempty"`
	CanRestoreRegistration bool                       `json:"canRestoreRegistration"`
	Saved                  bool                       `json:"saved"`
	Message                string                     `json:"message"`
}
type RecoveryCheckResult struct {
	Report  *BootstrapRecoveryReport `json:"report"`
	Code    string                   `json:"code"`
	Message string                   `json:"message"`
}
type RecoveryRegistrationReview struct {
	Token     string         `json:"token"`
	ExpiresAt time.Time      `json:"expiresAt"`
	Project   domain.Project `json:"project"`
}
type RecoveryRegistrationResult struct {
	Review  *RecoveryRegistrationReview `json:"review"`
	Code    string                      `json:"code"`
	Message string                      `json:"message"`
}
type pendingRecoveryRegistration struct {
	review RecoveryRegistrationReview
	target RecoveryTarget
	root   fileIdentity
	result *RegistryResult
}

var errRecoveryActive = errors.New("recovery owner is active")
var errRecoveryUntracked = errors.New("legacy operation has no checkpoint")
var errRecoveryManual = errors.New("recovery requires manual inspection")

// withRecovery pins a checkpoint and takes its nonblocking owner lease before
// the project-operation lock. No recovery path ever launches or stops tools.
func (service *Service) withRecovery(ctx context.Context, target RecoveryTarget, operation func(domain.BootstrapRecovery, *storage.Repository[domain.BootstrapRecovery], storage.Revision) error) error {
	if err := target.LogID.Validate(); err != nil {
		return err
	}
	if err := target.ProjectID.Validate(); err != nil {
		return err
	}
	if err := service.openRegistry(); err != nil {
		return err
	}
	kind, path := "", ""
	switch target.Scope {
	case "creation":
		repo, err := openCreationLogs(service.resolvedPaths)
		if err != nil {
			return err
		}
		log, err := repo.Get(ctx, target.LogID)
		repo.Close()
		if err != nil {
			return err
		}
		if log.Value.ProjectID != target.ProjectID {
			return errReview
		}
		kind, path = log.Value.Kind, log.Value.Path
	case "setup":
		repo, err := openSetupLogs(service.resolvedPaths, target.ProjectID)
		if err != nil {
			return err
		}
		log, err := repo.Get(ctx, target.LogID)
		repo.Close()
		if err != nil {
			return err
		}
		if log.Value.ProjectID != target.ProjectID {
			return errReview
		}
		kind = log.Value.Kind
	default:
		return errReview
	}
	repo, err := openRecoveryRecords(service.resolvedPaths)
	if err != nil {
		return err
	}
	defer repo.Close()
	snapshot, err := repo.Get(ctx, target.LogID)
	if errors.Is(err, fs.ErrNotExist) {
		return errRecoveryUntracked
	}
	if err != nil {
		return err
	}
	seed := snapshot.Value
	if seed.ProjectID != target.ProjectID || seed.Kind != kind || path != "" && seed.Path != path {
		return errReview
	}
	lease, err := recoveryLease(service.resolvedPaths, target.LogID)
	if errors.Is(err, storage.ErrLocked) {
		return errRecoveryActive
	}
	if err != nil {
		return err
	}
	defer lease.Close()
	return storage.WithDirectoryLock(ctx, service.operations, func() error { return operation(seed, repo, snapshot.Revision) })
}

func (service *Service) observeRecovery(ctx context.Context, seed domain.BootstrapRecovery) (BootstrapRecoveryReport, *os.File, error) {
	report := BootstrapRecoveryReport{Owner: "released", Path: seed.Path}
	assessment := &domain.RecoveryAssessment{CheckedAt: service.now().UTC(), Destination: "unsafe", Registration: "missing", Files: "not_checked", SetupOutput: "not_applicable"}
	report.Assessment = assessment
	records, err := service.registry.List(ctx)
	if err != nil {
		return report, nil, err
	}
	for _, record := range records {
		if record.Value.ID == seed.ProjectID || record.Value.Path == seed.Path {
			if record.Value.ID == seed.ProjectID && record.Value.Path == seed.Path && assessment.Registration != "conflict" {
				assessment.Registration = "matching"
			} else {
				assessment.Registration = "conflict"
			}
		}
	}
	directory, err := openSetupDirectory(seed.Path)
	if err == nil {
		assessment.Destination = "directory"
	} else {
		parent, parentErr := bootstrap.OpenParent(filepath.Dir(seed.Path))
		if parentErr == nil {
			if parent.Absent(filepath.Base(seed.Path)) == nil {
				assessment.Destination = "missing"
			}
			parent.Close()
		}
	}
	if directory != nil && seed.Kind == "publication" && assessment.Registration == "missing" {
		assessment.Files = "changed"
		if err := bootstrap.VerifyRecoveryTree(ctx, directory, seed.Files); err == nil {
			assessment.Files = "matching"
		} else if ctx.Err() != nil {
			directory.Close()
			return report, nil, ctx.Err()
		}
	}
	if seed.Kind == "git" || seed.Kind == "environment" || seed.Kind == "dependencies" {
		assessment.SetupOutput = "unsafe"
		if directory != nil {
			paths := []string{".git"}
			if seed.Kind == "environment" {
				paths = []string{".venv"}
			}
			if seed.Kind == "dependencies" {
				paths = []string{"node_modules", ".venv"}
			}
			assessment.SetupOutput = "absent"
			for _, path := range paths {
				var stat unix.Stat_t
				err := unix.Fstatat(int(directory.Fd()), path, &stat, unix.AT_SYMLINK_NOFOLLOW)
				if errors.Is(err, unix.ENOENT) {
					continue
				}
				if err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR {
					assessment.SetupOutput = "unsafe"
					break
				}
				assessment.SetupOutput = "present"
			}
		}
	}
	report.CanRestoreRegistration = seed.Kind == "publication" && assessment.Destination == "directory" && assessment.Registration == "missing" && assessment.Files == "matching"
	switch {
	case assessment.Registration == "conflict":
		assessment.Message = "A different registration uses this identity or destination. Existing records and files were kept; inspect and resolve the conflict manually."
	case report.CanRestoreRegistration:
		assessment.Message = "The current directory exactly matches the saved file hashes. Review the original configuration to restore its registration with startup commands unapproved."
	case seed.Kind == "git" || seed.Kind == "environment" || seed.Kind == "dependencies":
		assessment.Message = "The attempt's original app owner released its lease. Files or directories do not prove setup completed. Preserve partial output and inspect it before any manual setup; commands will not retry automatically."
	case assessment.Registration == "matching":
		assessment.Message = "The original registration is present. Reload registered projects. Source changes and setup success are not inferred from this check."
	case assessment.Destination == "missing":
		assessment.Message = "The destination is absent. No operation will resume. Preparation output was temporary; request a new creation review when ready."
	case seed.Kind == "publication":
		assessment.Message = "The current destination could not be verified as the reviewed starter. Preserve it and use project inspection and reviewed registration manually."
	default:
		assessment.Message = "The attempt's original app owner released its lease. Files or directories do not prove setup completed. Preserve partial output and inspect it before any manual setup; commands will not retry automatically."
	}
	report.Message = assessment.Message
	return report, directory, nil
}

// CheckBootstrapRecovery stores observations separately from the original log.
// A released owner lease is not evidence of a successful exit or child cleanup.
func (service *Service) CheckBootstrapRecovery(ctx context.Context, target RecoveryTarget) RecoveryCheckResult {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	service.mu.Lock()
	defer service.mu.Unlock()
	var report BootstrapRecoveryReport
	err := service.withRecovery(ctx, target, func(seed domain.BootstrapRecovery, repo *storage.Repository[domain.BootstrapRecovery], revision storage.Revision) error {
		var directory *os.File
		var err error
		report, directory, err = service.observeRecovery(ctx, seed)
		if directory != nil {
			defer directory.Close()
		}
		if err != nil {
			return err
		}
		if seed.Assessment != nil && report.Assessment.CheckedAt.Before(seed.Assessment.CheckedAt) {
			report.Assessment.CheckedAt = seed.Assessment.CheckedAt
		}
		err = repo.Update(ctx, seed.ID, revision, func(value domain.BootstrapRecovery) (domain.BootstrapRecovery, error) {
			value.Assessment = report.Assessment
			return value, nil
		})
		report.Saved = err == nil
		return err
	})
	if errors.Is(err, errRecoveryActive) {
		return RecoveryCheckResult{Report: &BootstrapRecoveryReport{Owner: "active", Message: "Another live app handle owns this attempt. Wait for it to finish before checking recovery. No files or registrations were changed."}}
	}
	if errors.Is(err, errRecoveryUntracked) {
		return RecoveryCheckResult{Report: &BootstrapRecoveryReport{Owner: "untracked", Message: "This older attempt has no recovery checkpoint or owner lease. Inspect its destination and registrations manually; its running state cannot be established."}}
	}
	if err != nil {
		result := recoveryFailure(err)
		if report.Assessment != nil {
			result.Report = &report
		}
		return result
	}
	return RecoveryCheckResult{Report: &report}
}

func (service *Service) ReviewRecoveryRegistration(ctx context.Context, target RecoveryTarget) RecoveryRegistrationResult {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	service.mu.Lock()
	defer service.mu.Unlock()
	var review RecoveryRegistrationReview
	err := service.withRecovery(ctx, target, func(seed domain.BootstrapRecovery, _ *storage.Repository[domain.BootstrapRecovery], _ storage.Revision) error {
		report, directory, err := service.observeRecovery(ctx, seed)
		if directory != nil {
			defer directory.Close()
		}
		if err != nil {
			return err
		}
		if !report.CanRestoreRegistration {
			return errRecoveryManual
		}
		root, err := identity(seed.Path, false)
		if err != nil {
			return err
		}
		token, err := domain.NewID()
		if err != nil {
			return err
		}
		project := cloneProject(*seed.Project)
		for i := range project.Commands {
			project.Commands[i].Approval = nil
		}
		project.UpdatedAt = service.now().UTC()
		if project.UpdatedAt.Before(project.CreatedAt) {
			project.UpdatedAt = project.CreatedAt
		}
		review = RecoveryRegistrationReview{Token: string(token), ExpiresAt: service.now().UTC().Add(5 * time.Minute), Project: project}
		if service.recoveryReviews == nil {
			service.recoveryReviews = map[string]*pendingRecoveryRegistration{}
		}
		for key, pending := range service.recoveryReviews {
			if !service.now().Before(pending.review.ExpiresAt) {
				delete(service.recoveryReviews, key)
			}
		}
		if len(service.recoveryReviews) >= 64 {
			return errReview
		}
		service.recoveryReviews[review.Token] = &pendingRecoveryRegistration{review: review, target: target, root: root}
		return nil
	})
	if err != nil {
		failure := recoveryFailure(err)
		return RecoveryRegistrationResult{Code: failure.Code, Message: failure.Message}
	}
	review.Project = cloneProject(review.Project)
	return RecoveryRegistrationResult{Review: &review}
}

func (service *Service) CommitRecoveryRegistration(ctx context.Context, token string) RegistryResult {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		return registryFailure(storage.ErrClosed)
	}
	pending := service.recoveryReviews[token]
	if pending == nil {
		return registryFailure(errReview)
	}
	if pending.result != nil {
		return cloneResult(*pending.result)
	}
	if !service.now().Before(pending.review.ExpiresAt) {
		return registryFailure(errReview)
	}
	result := RegistryResult{}
	err := service.withRecovery(ctx, pending.target, func(seed domain.BootstrapRecovery, _ *storage.Repository[domain.BootstrapRecovery], _ storage.Revision) error {
		report, directory, err := service.observeRecovery(ctx, seed)
		if directory != nil {
			defer directory.Close()
		}
		if err != nil {
			return err
		}
		if !report.CanRestoreRegistration {
			return errRecoveryManual
		}
		root, err := identity(seed.Path, false)
		if err != nil || root != pending.root {
			return errReview
		}
		err = service.registry.Create(ctx, pending.review.Project)
		var mutation *storage.MutationError
		result.Committed = err == nil || errors.As(err, &mutation) && mutation.Committed
		if err != nil {
			return err
		}
		result.State, err = service.registryState(ctx)
		return err
	})
	if err != nil {
		failure := registryFailure(err)
		failure.Committed = result.Committed
		result = failure
	}
	if result.Committed && result.Message == "" {
		result.Message = "Registration restored. Startup commands remain unapproved; no source files or setup output were changed."
	}
	pending.result = &result
	return cloneResult(result)
}

// GetRecoveryRegistrationOutcome never turns a lost response into a new write.
func (service *Service) GetRecoveryRegistrationOutcome(ctx context.Context, token string) RegistryResult {
	service.mu.Lock()
	defer service.mu.Unlock()
	if ctx.Err() != nil {
		return registryFailure(ctx.Err())
	}
	if service.closed {
		return registryFailure(storage.ErrClosed)
	}
	pending := service.recoveryReviews[token]
	if pending == nil {
		return registryFailure(errReview)
	}
	if pending.result != nil {
		return cloneResult(*pending.result)
	}
	return RegistryResult{Code: "recovery_pending", Message: "No completed restore outcome is available. Check again or recheck recovery before requesting a new review."}
}
func recoveryFailure(err error) RecoveryCheckResult {
	switch {
	case errors.Is(err, errRecoveryActive):
		return RecoveryCheckResult{Code: "recovery_active", Message: "The original app still owns this attempt. Wait for it to finish."}
	case errors.Is(err, errRecoveryUntracked):
		return RecoveryCheckResult{Code: "recovery_legacy", Message: "This attempt has no saved recovery checkpoint. Inspect and register the project manually."}
	case errors.Is(err, errRecoveryManual):
		return RecoveryCheckResult{Code: "recovery_manual", Message: "This attempt cannot restore a registration from its current evidence. Recheck recovery and inspect the destination manually."}
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return RecoveryCheckResult{Code: "cancelled", Message: "Recovery checking was cancelled or timed out. Existing files and records were kept."}
	default:
		return RecoveryCheckResult{Code: "recovery_storage", Message: "Recovery records or project metadata could not be verified. Existing files and records were kept; check permissions and stored data."}
	}
}
