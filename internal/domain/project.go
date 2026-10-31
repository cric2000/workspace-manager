package domain

import (
	"errors"
	"fmt"
	"time"
)

type ProjectSource string

const (
	SourceImported     ProjectSource = "imported"
	SourceBootstrapped ProjectSource = "bootstrapped"
)

// Project is the canonical registration of a local codebase.
// Raw environment values and process IDs do not belong in this record.
type Project struct {
	ID          ID                  `json:"id"`
	Name        string              `json:"name"`
	Path        string              `json:"path"`
	Source      ProjectSource       `json:"source"`
	Commands    []StartupCommand    `json:"commands,omitempty"`
	Preferences *ProjectPreferences `json:"preferences,omitempty"`
	CreatedAt   time.Time           `json:"createdAt"`
	UpdatedAt   time.Time           `json:"updatedAt"`
}

func (project Project) Validate() error {
	if err := project.ID.Validate(); err != nil {
		return fmt.Errorf("id: %w", err)
	}
	if err := validateName(project.Name); err != nil {
		return fmt.Errorf("name: %w", err)
	}
	if err := validatePath(project.Path); err != nil {
		return fmt.Errorf("path: %w", err)
	}
	if project.Source != SourceImported && project.Source != SourceBootstrapped {
		return errors.New("source must be imported or bootstrapped")
	}
	if len(project.Commands) > 16 {
		return errors.New("at most 16 startup commands are allowed")
	}
	if project.Preferences != nil {
		if err := project.Preferences.Validate(); err != nil {
			return fmt.Errorf("preferences: %w", err)
		}
	}
	seen := make(map[ID]bool)
	for index, command := range project.Commands {
		if err := command.Validate(); err != nil {
			return fmt.Errorf("commands[%d]: %w", index, err)
		}
		if seen[command.ID] {
			return errors.New("duplicate startup command ID")
		}
		seen[command.ID] = true
	}
	return validateTimes(project.CreatedAt, project.UpdatedAt)
}
