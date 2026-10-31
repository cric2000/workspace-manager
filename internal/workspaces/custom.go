package workspaces

import (
	"context"

	"workspace-manager/internal/applications"
	"workspace-manager/internal/domain"
)

// NewCustomResolver exposes only saved, revision-matched configuration to the
// launcher. It is not a Wails method; the launch bridge accepts a target, not argv.
func NewCustomResolver(service *Service) applications.CustomResolver {
	return func(ctx context.Context, target applications.CustomTarget) (domain.CustomApplication, error) {
		service.mu.Lock()
		defer service.mu.Unlock()
		if validTarget(Target{ID: target.WorkspaceID, Revision: target.Revision}) != nil || target.CustomID.Validate() != nil {
			return domain.CustomApplication{}, applications.ErrUnavailable
		}
		if err := service.open(); err != nil {
			return domain.CustomApplication{}, err
		}
		record, err := service.profiles.Get(ctx, target.WorkspaceID)
		if err != nil {
			return domain.CustomApplication{}, err
		}
		if record.Revision != target.Revision {
			return domain.CustomApplication{}, applications.ErrLaunchChanged
		}
		for _, choice := range record.Value.Applications {
			if choice.Custom != nil && choice.Custom.ID == target.CustomID {
				return *domain.CloneApplications([]domain.WorkspaceApplication{choice})[0].Custom, nil
			}
		}
		return domain.CustomApplication{}, applications.ErrUnavailable
	}
}
