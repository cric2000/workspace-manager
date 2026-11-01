package projects

import (
	"context"
	"fmt"
	"strings"

	"workspace-manager/internal/storage"
)

// RemoveProject removes only a registration through the repository's recoverable
// delete. Source directories and independent session snapshots are never touched.
// Lock order: workspace operations -> project operations -> repository locks.
func (service *Service) RemoveProject(ctx context.Context, target ProjectTarget) RegistryResult {
	if target.ID.Validate() != nil || !validRevision(target.Revision) {
		return RegistryResult{Code: "validation", Message: "Reload registered projects and choose the registration to remove."}
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.openRegistry(); err != nil {
		return registryFailure(err)
	}
	workspaceLock, err := storage.OpenPrivateDirectory(service.resolvedPaths.WorkspaceOperationsDir())
	if err != nil {
		return referenceFailure()
	}
	defer workspaceLock.Close()
	var result RegistryResult
	err = storage.WithDirectoryLock(ctx, workspaceLock, func() error {
		return storage.WithDirectoryLock(ctx, service.operations, func() error {
			current, err := service.registry.Get(ctx, target.ID)
			if err != nil {
				return err
			}
			if current.Revision != target.Revision {
				return storage.ErrConflict
			}
			codec, err := storage.NewWorkspaceCodec()
			if err != nil {
				return err
			}
			profiles, err := storage.OpenRepository(service.resolvedPaths.WorkspacesDir(), codec)
			if err != nil {
				result = referenceFailure()
				return nil
			}
			defer profiles.Close()
			workspaces, err := profiles.List(ctx)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				result = referenceFailure()
				return nil
			}
			names := []string{}
			count := 0
			for _, workspace := range workspaces {
				for _, id := range workspace.Value.ProjectIDs {
					if id == target.ID {
						count++
						if len(names) < 5 {
							names = append(names, workspace.Value.Name)
						}
						break
					}
				}
			}
			if count > 0 {
				message := "Remove this project from its workspace profiles first: " + strings.Join(names, ", ") + "."
				if count > len(names) {
					message += fmt.Sprintf(" %d additional profiles also reference it.", count-len(names))
				}
				result = RegistryResult{Code: "project_in_use", Message: message}
				return nil
			}
			if err := service.registry.Delete(ctx, target.ID, target.Revision); err != nil {
				return err
			}
			result.Committed = true
			result.State, err = service.registryState(ctx)
			if err != nil {
				result.Code, result.Message = "refresh_required", "Registration removed, but the list could not refresh. Reload registered projects before making another change."
			}
			return nil
		})
	})
	if err != nil {
		return registryFailure(err)
	}
	return result
}

func referenceFailure() RegistryResult {
	return RegistryResult{Code: "references_unavailable", Message: "Workspace references could not be checked. Existing files were preserved. Repair or open compatible workspace storage before removing this registration."}
}
