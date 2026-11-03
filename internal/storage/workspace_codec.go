package storage

import (
	"encoding/json"
	"time"

	"workspace-manager/internal/domain"
)

const WorkspaceSchemaVersion = 4

// NewWorkspaceCodec reads schema 1 without writing it on load. The next explicit
// mutation archives its exact original bytes before writing schema 3.
func NewWorkspaceCodec() (*Codec[domain.Workspace], error) {
	return NewCodec[domain.Workspace](WorkspaceSchemaVersion, map[int]Migration{1: migrateWorkspaceV1, 2: migrateWorkspaceV2, 3: migrateWorkspaceV3})
}

func migrateWorkspaceV3(payload json.RawMessage) (json.RawMessage, error) {
	var old struct {
		ID           domain.ID                     `json:"id"`
		Name         string                        `json:"name"`
		Description  string                        `json:"description"`
		ProjectIDs   []domain.ID                   `json:"projectIds"`
		Applications []domain.WorkspaceApplication `json:"applications"`
		CreatedAt    time.Time                     `json:"createdAt"`
		UpdatedAt    time.Time                     `json:"updatedAt"`
	}
	if err := decodeFields(payload, &old); err != nil {
		return nil, err
	}
	return json.Marshal(domain.Workspace{ID: old.ID, Name: old.Name, Description: old.Description, ProjectIDs: old.ProjectIDs, Applications: old.Applications, CreatedAt: old.CreatedAt, UpdatedAt: old.UpdatedAt})
}

func migrateWorkspaceV2(payload json.RawMessage) (json.RawMessage, error) {
	// Exact schema-2 shape: custom commands in an old envelope are not approved
	// by migration. Unknown fields fail closed, including nested application data.
	var old struct {
		ID           domain.ID   `json:"id"`
		Name         string      `json:"name"`
		Description  string      `json:"description"`
		ProjectIDs   []domain.ID `json:"projectIds"`
		Applications []struct {
			DesktopID       domain.DesktopID       `json:"desktopId"`
			DuplicatePolicy domain.DuplicatePolicy `json:"duplicatePolicy"`
		} `json:"applications"`
		CreatedAt time.Time `json:"createdAt"`
		UpdatedAt time.Time `json:"updatedAt"`
	}
	if err := decodeFields(payload, &old); err != nil {
		return nil, err
	}
	value := domain.Workspace{ID: old.ID, Name: old.Name, Description: old.Description, ProjectIDs: old.ProjectIDs, CreatedAt: old.CreatedAt, UpdatedAt: old.UpdatedAt, Applications: make([]domain.WorkspaceApplication, 0, len(old.Applications))}
	for _, app := range old.Applications {
		value.Applications = append(value.Applications, domain.WorkspaceApplication{DesktopID: app.DesktopID, DuplicatePolicy: app.DuplicatePolicy})
	}
	if value.Validate() != nil {
		return nil, ErrInvalidDocument
	}
	return json.Marshal(value)
}

func migrateWorkspaceV1(payload json.RawMessage) (json.RawMessage, error) {
	// Keep the exact old shape: schema-1 unknown fields must not be discarded or
	// treated as newly approved application settings during migration.
	var old struct {
		ID          domain.ID   `json:"id"`
		Name        string      `json:"name"`
		Description string      `json:"description"`
		ProjectIDs  []domain.ID `json:"projectIds"`
		CreatedAt   time.Time   `json:"createdAt"`
		UpdatedAt   time.Time   `json:"updatedAt"`
	}
	if err := decodeFields(payload, &old); err != nil {
		return nil, err
	}
	value := domain.Workspace{ID: old.ID, Name: old.Name, Description: old.Description, ProjectIDs: old.ProjectIDs, Applications: []domain.WorkspaceApplication{}, CreatedAt: old.CreatedAt, UpdatedAt: old.UpdatedAt}
	if err := value.Validate(); err != nil {
		return nil, ErrInvalidDocument
	}
	return json.Marshal(value)
}
