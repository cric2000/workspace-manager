package domain

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// Session is a snapshot, not a workspace profile. Projects are values as captured,
// so later registry edits cannot silently change the saved configuration. These
// records contain metadata, not physical copies of the project directories.
// Repository-level immutability and revision history are implemented separately.
type Session struct {
	ID              ID                     `json:"id"`
	Name            string                 `json:"name"`
	CapturedAt      time.Time              `json:"capturedAt"`
	WorkspaceID     ID                     `json:"workspaceId,omitempty"`
	ParentSessionID ID                     `json:"parentSessionId,omitempty"`
	Projects        []Project              `json:"projects"`
	Applications    []WorkspaceApplication `json:"applications,omitempty"`
	URLs            []string               `json:"urls,omitempty"`
	CaptureWarnings []string               `json:"captureWarnings"`
}

func (session Session) Validate() error {
	if err := session.ID.Validate(); err != nil {
		return fmt.Errorf("id: %w", err)
	}
	if err := validateName(session.Name); err != nil {
		return fmt.Errorf("name: %w", err)
	}
	if err := validateTime(session.CapturedAt); err != nil {
		return fmt.Errorf("capturedAt: %w", err)
	}
	if err := validateOptionalID(session.WorkspaceID); err != nil {
		return fmt.Errorf("workspaceId: %w", err)
	}
	if err := validateOptionalID(session.ParentSessionID); err != nil {
		return fmt.Errorf("parentSessionId: %w", err)
	}
	if session.ParentSessionID == session.ID {
		return errors.New("parentSessionId must not refer to the session itself")
	}
	seen := make(map[ID]bool, len(session.Projects))
	if len(session.Applications) > MaxWorkspaceApplications || len(session.URLs) > 64 || len(session.Projects) > 256 {
		return errors.New("session exceeds capture limits")
	}
	for _, app := range session.Applications {
		if err := app.Validate(); err != nil {
			return err
		}
	}
	for _, url := range session.URLs {
		if err := ValidateWebURL(url); err != nil {
			return err
		}
	}
	for index, project := range session.Projects {
		if err := project.Validate(); err != nil {
			return fmt.Errorf("projects[%d]: %w", index, err)
		}
		if seen[project.ID] {
			return fmt.Errorf("projects[%d]: duplicate project snapshot", index)
		}
		seen[project.ID] = true
	}
	for index, warning := range session.CaptureWarnings {
		if err := validateText(warning, 4096); err != nil {
			return fmt.Errorf("captureWarnings[%d]: %w", index, err)
		}
	}
	return nil
}

// Branch returns an independent snapshot with a new identity. It performs no
// capture, persistence, command execution or filesystem changes.
func (session Session) Branch(name string, capturedAt time.Time) (Session, error) {
	if err := session.Validate(); err != nil {
		return Session{}, fmt.Errorf("source session: %w", err)
	}
	branch := session
	branch.ID = ""
	branch.Name = name
	branch.CapturedAt = capturedAt.UTC()
	branch.ParentSessionID = session.ID
	branch.Projects = slices.Clone(session.Projects)
	for i := range branch.Projects {
		branch.Projects[i].Commands = CloneCommands(session.Projects[i].Commands)
		branch.Projects[i].Preferences = ClonePreferences(session.Projects[i].Preferences)
	}
	branch.CaptureWarnings = slices.Clone(session.CaptureWarnings)
	branch.Applications = CloneApplications(session.Applications)
	branch.URLs = slices.Clone(session.URLs)
	var err error
	branch.ID, err = NewID()
	if err != nil {
		return Session{}, fmt.Errorf("create session ID: %w", err)
	}
	if err := branch.Validate(); err != nil {
		return Session{}, err
	}
	return branch, nil
}
