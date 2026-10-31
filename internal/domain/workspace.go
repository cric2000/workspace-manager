package domain

import (
	"fmt"
	"time"
)

// Workspace is an editable profile. Projects are referenced, never copied here.
// Browser/startup fields will be added with their feature services.
type Workspace struct {
	ID           ID                     `json:"id"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	ProjectIDs   []ID                   `json:"projectIds"`
	Applications []WorkspaceApplication `json:"applications"`
	CreatedAt    time.Time              `json:"createdAt"`
	UpdatedAt    time.Time              `json:"updatedAt"`
}

func (workspace Workspace) Validate() error {
	if err := workspace.ID.Validate(); err != nil {
		return fmt.Errorf("id: %w", err)
	}
	if err := validateName(workspace.Name); err != nil {
		return fmt.Errorf("name: %w", err)
	}
	if err := validateText(workspace.Description, 4096); err != nil {
		return fmt.Errorf("description: %w", err)
	}
	if err := validateTimes(workspace.CreatedAt, workspace.UpdatedAt); err != nil {
		return err
	}
	seen := make(map[ID]bool, len(workspace.ProjectIDs))
	for index, id := range workspace.ProjectIDs {
		if err := id.Validate(); err != nil {
			return fmt.Errorf("projectIds[%d]: %w", index, err)
		}
		if seen[id] {
			return fmt.Errorf("projectIds[%d]: duplicate project reference", index)
		}
		seen[id] = true
	}
	if len(workspace.Applications) > MaxWorkspaceApplications {
		return fmt.Errorf("applications: at most %d applications are allowed", MaxWorkspaceApplications)
	}
	apps := make(map[string]bool, len(workspace.Applications))
	for index, application := range workspace.Applications {
		if err := application.Validate(); err != nil {
			return fmt.Errorf("applications[%d]: %w", index, err)
		}
		key := "desktop:" + string(application.DesktopID)
		if application.Custom != nil {
			key = "custom:" + string(application.Custom.ID)
		}
		if apps[key] {
			return fmt.Errorf("applications[%d]: duplicate application identity", index)
		}
		apps[key] = true
	}
	return nil
}
