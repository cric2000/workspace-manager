package projects

import (
	"context"
	"slices"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/projects/detection"
	"workspace-manager/internal/storage"
)

type ProjectTarget struct {
	ID       domain.ID        `json:"id"`
	Revision storage.Revision `json:"revision"`
}

// CandidateComparison describes invocation equality, never source equality,
// readiness, approval or a recommended automatic replacement.
type CandidateComparison struct {
	ResolvedExecutable string      `json:"resolvedExecutable"`
	Available          bool        `json:"available"`
	MatchingCommandIDs []domain.ID `json:"matchingCommandIds"`
}

type ProjectComparison struct {
	Saved      ProjectView           `json:"saved"`
	Report     detection.Report      `json:"report"`
	Candidates []CandidateComparison `json:"candidates"`
	ScannedAt  time.Time             `json:"scannedAt"`
}

type RescanResult struct {
	Comparison *ProjectComparison `json:"comparison"`
	Code       string             `json:"code"`
	Message    string             `json:"message"`
}

func (service *Service) rescanSnapshot(ctx context.Context, target ProjectTarget) (ProjectView, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.openRegistry(); err != nil {
		return ProjectView{}, err
	}
	snapshot, err := service.registry.Get(ctx, target.ID)
	if err != nil {
		return ProjectView{}, err
	}
	if snapshot.Revision != target.Revision {
		return ProjectView{}, storage.ErrConflict
	}
	return ProjectView{Project: snapshot.Value, Revision: snapshot.Revision}, nil
}

// RescanProject reads a revision-checked registration and inspects its directory.
// It holds no repository/service lock while inspecting, writes no record and
// issues no approval. Selection/editing uses the existing reviewed save flow.
func (service *Service) RescanProject(ctx context.Context, target ProjectTarget) RescanResult {
	if target.ID.Validate() != nil || !validRevision(target.Revision) {
		return RescanResult{Code: "validation", Message: "Reload registered projects and choose a saved project to compare."}
	}
	saved, err := service.rescanSnapshot(ctx, target)
	if err != nil {
		return rescanFailure(err)
	}
	path, err := canonicalDirectory(saved.Project.Path)
	if err != nil || path != saved.Project.Path {
		return rescanFailure(errDirectory)
	}
	before, err := identity(path, false)
	if err != nil {
		return rescanFailure(errDirectory)
	}
	if service.inspector == nil {
		return RescanResult{Code: "inspection_failed", Message: "The project inspector is unavailable. No saved configuration was changed."}
	}
	inspection := service.Inspect(ctx, path)
	if inspection.Code != "" || inspection.Report == nil {
		return RescanResult{Code: inspection.Code, Message: inspection.Message}
	}
	if inspection.Report.Path != path {
		return changedDirectory()
	}
	comparison := &ProjectComparison{Saved: saved, Report: *inspection.Report, Candidates: make([]CandidateComparison, 0, len(inspection.Report.Candidates))}
	for _, candidate := range inspection.Report.Candidates {
		if err := ctx.Err(); err != nil {
			return rescanFailure(err)
		}
		status := CandidateComparison{MatchingCommandIDs: []domain.ID{}}
		action, err := normalizeAction(domain.CommandAction{Executable: candidate.Program, Arguments: candidate.Arguments, WorkingDirectory: candidate.WorkingDirectory}, path)
		if err == nil {
			_, err = identity(action.Executable, true)
			if err == nil {
				status.Available, status.ResolvedExecutable = true, action.Executable
				for _, command := range saved.Project.Commands {
					configuration := command.Configuration
					if configuration.Executable == action.Executable && configuration.WorkingDirectory == action.WorkingDirectory && slices.Equal(configuration.Arguments, action.Arguments) {
						status.MatchingCommandIDs = append(status.MatchingCommandIDs, command.ID)
					}
				}
			}
		}
		comparison.Candidates = append(comparison.Candidates, status)
	}
	after, err := identity(path, false)
	if err != nil || after != before {
		return changedDirectory()
	}
	// A different GUI can edit/delete the registration during inspection. Do not
	// return a comparison against an obsolete saved revision in that case.
	if _, err := service.rescanSnapshot(ctx, target); err != nil {
		return rescanFailure(err)
	}
	if err := ctx.Err(); err != nil {
		return rescanFailure(err)
	}
	comparison.ScannedAt = service.now().UTC()
	return RescanResult{Comparison: comparison}
}

func rescanFailure(err error) RescanResult {
	failure := registryFailure(err)
	return RescanResult{Code: failure.Code, Message: failure.Message}
}

func changedDirectory() RescanResult {
	return RescanResult{Code: "directory_changed", Message: "The project directory changed during inspection. Check its path and rescan. Saved configuration was not changed."}
}
