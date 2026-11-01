package workspaces

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

type ProjectSelection struct {
	ID       domain.ID        `json:"id"`
	Revision storage.Revision `json:"revision"`
}

var (
	errProjectSelection   = errors.New("invalid project selection")
	errProjectUnavailable = errors.New("project registration unavailable")
	errProjectStorage     = errors.New("project storage unavailable")
	errProjectChanged     = errors.New("selected project changed")
)

// SetProjects stores ordered references, not copies or command approval. The
// workspace operation lock also excludes guarded project removal and duplication.
// Existing missing references can be retained or explicitly removed, never added.
func (service *Service) SetProjects(ctx context.Context, target Target, selection []ProjectSelection) Result {
	if len(selection) > 64 {
		return failure(errProjectSelection)
	}
	choices := slices.Clone(selection)
	seen := make(map[domain.ID]bool, len(choices))
	for _, choice := range choices {
		if choice.ID.Validate() != nil || seen[choice.ID] {
			return failure(errProjectSelection)
		}
		seen[choice.ID] = true
	}
	return service.perform(ctx, func(_ *State) error {
		if err := validTarget(target); err != nil {
			return err
		}
		codec, err := storage.NewProjectCodec()
		if err != nil {
			return err
		}
		registry, err := storage.OpenRepository(service.resolvedPaths.ProjectsDir(), codec)
		if err != nil {
			return errProjectStorage
		}
		defer registry.Close()
		return service.profiles.Update(ctx, target.ID, target.Revision, func(value domain.Workspace) (domain.Workspace, error) {
			ids := make([]domain.ID, 0, len(choices))
			for _, choice := range choices {
				project, err := registry.Get(ctx, choice.ID)
				if errors.Is(err, fs.ErrNotExist) && slices.Contains(value.ProjectIDs, choice.ID) {
					ids = append(ids, choice.ID)
					continue
				}
				if ctx.Err() != nil {
					return value, ctx.Err()
				}
				if errors.Is(err, fs.ErrNotExist) {
					return value, errProjectUnavailable
				}
				if err != nil {
					return value, errProjectStorage
				}
				if validTarget(Target{ID: choice.ID, Revision: choice.Revision}) != nil {
					return value, errProjectSelection
				}
				if choice.Revision != project.Revision {
					return value, errProjectChanged
				}
				ids = append(ids, choice.ID)
			}
			value.ProjectIDs = ids
			if now := time.Now().UTC(); now.After(value.UpdatedAt) {
				value.UpdatedAt = now
			}
			return value, nil
		})
	})
}
