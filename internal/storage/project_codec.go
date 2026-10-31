package storage

import (
	"encoding/json"
	"time"

	"workspace-manager/internal/domain"
)

const ProjectSchemaVersion = 3
const SessionSchemaVersion = 3

// Legacy records migrate in memory. Only an explicit mutation writes the new
// envelope, preserving the exact previous bytes in history. No approval is inferred.
func NewProjectCodec() (*Codec[domain.Project], error) {
	return NewCodec[domain.Project](ProjectSchemaVersion, map[int]Migration{1: migrateProjectV1, 2: migrateProjectV2})
}

func NewSessionCodec() (*Codec[domain.Session], error) {
	return NewCodec[domain.Session](SessionSchemaVersion, map[int]Migration{1: migrateSessionV1, 2: migrateSessionV2})
}

type projectV1 struct {
	ID        domain.ID            `json:"id"`
	Name      string               `json:"name"`
	Path      string               `json:"path"`
	Source    domain.ProjectSource `json:"source"`
	CreatedAt time.Time            `json:"createdAt"`
	UpdatedAt time.Time            `json:"updatedAt"`
}

func (old projectV1) current() domain.Project {
	return domain.Project{ID: old.ID, Name: old.Name, Path: old.Path, Source: old.Source, CreatedAt: old.CreatedAt, UpdatedAt: old.UpdatedAt}
}

func migrateProjectV1(payload json.RawMessage) (json.RawMessage, error) {
	var old projectV1
	if err := decodeFields(payload, &old); err != nil {
		return nil, err
	}
	return json.Marshal(old.current())
}

func migrateSessionV1(payload json.RawMessage) (json.RawMessage, error) {
	var old struct {
		ID              domain.ID   `json:"id"`
		Name            string      `json:"name"`
		CapturedAt      time.Time   `json:"capturedAt"`
		WorkspaceID     domain.ID   `json:"workspaceId,omitempty"`
		ParentSessionID domain.ID   `json:"parentSessionId,omitempty"`
		Projects        []projectV1 `json:"projects"`
		CaptureWarnings []string    `json:"captureWarnings"`
	}
	if err := decodeFields(payload, &old); err != nil {
		return nil, err
	}
	value := domain.Session{ID: old.ID, Name: old.Name, CapturedAt: old.CapturedAt, WorkspaceID: old.WorkspaceID, ParentSessionID: old.ParentSessionID, CaptureWarnings: old.CaptureWarnings}
	if old.Projects != nil {
		value.Projects = make([]domain.Project, len(old.Projects))
	}
	for i, project := range old.Projects {
		value.Projects[i] = project.current()
	}
	return json.Marshal(value)
}

// Keep the exact schema-2 shape. Newly introduced settings cannot be smuggled
// through an older envelope, nor silently acquire approval during migration.
type projectV2 struct {
	projectV1
	Commands []struct {
		ID            domain.ID `json:"id"`
		Configuration struct {
			Name             string   `json:"name"`
			Executable       string   `json:"executable"`
			Arguments        []string `json:"arguments"`
			WorkingDirectory string   `json:"workingDirectory"`
		} `json:"configuration"`
		Approval *domain.CommandApproval `json:"approval,omitempty"`
	} `json:"commands,omitempty"`
}

func (old projectV2) current() domain.Project {
	value := old.projectV1.current()
	if old.Commands != nil {
		value.Commands = make([]domain.StartupCommand, 0, len(old.Commands))
	}
	for _, command := range old.Commands {
		configuration := command.Configuration
		value.Commands = append(value.Commands, domain.StartupCommand{ID: command.ID, Approval: command.Approval, Configuration: domain.CommandConfiguration{Name: configuration.Name, Executable: configuration.Executable, Arguments: configuration.Arguments, WorkingDirectory: configuration.WorkingDirectory}})
	}
	return value
}

func migrateProjectV2(payload json.RawMessage) (json.RawMessage, error) {
	var old projectV2
	if err := decodeFields(payload, &old); err != nil {
		return nil, err
	}
	return json.Marshal(old.current())
}

func migrateSessionV2(payload json.RawMessage) (json.RawMessage, error) {
	var old struct {
		ID              domain.ID   `json:"id"`
		Name            string      `json:"name"`
		CapturedAt      time.Time   `json:"capturedAt"`
		WorkspaceID     domain.ID   `json:"workspaceId,omitempty"`
		ParentSessionID domain.ID   `json:"parentSessionId,omitempty"`
		Projects        []projectV2 `json:"projects"`
		CaptureWarnings []string    `json:"captureWarnings"`
	}
	if err := decodeFields(payload, &old); err != nil {
		return nil, err
	}
	value := domain.Session{ID: old.ID, Name: old.Name, CapturedAt: old.CapturedAt, WorkspaceID: old.WorkspaceID, ParentSessionID: old.ParentSessionID, CaptureWarnings: old.CaptureWarnings}
	if old.Projects != nil {
		value.Projects = make([]domain.Project, len(old.Projects))
	}
	for i, project := range old.Projects {
		value.Projects[i] = project.current()
	}
	return json.Marshal(value)
}
