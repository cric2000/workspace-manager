package domain

import (
	"errors"
	"time"
)

// ManagedRun is controller-owned runtime metadata. It is never a project profile
// and contains neither inherited environment nor environment file contents.
type ManagedRun struct {
	ID                 ID             `json:"id"`
	ProjectID          ID             `json:"projectId"`
	Command            StartupCommand `json:"command"`
	WorkspaceID        ID             `json:"workspaceId,omitempty"`
	LaunchID           ID             `json:"launchId,omitempty"`
	StartedAt          time.Time      `json:"startedAt"`
	UpdatedAt          time.Time      `json:"updatedAt"`
	State              string         `json:"state"`
	WorkerPID          int            `json:"workerPid"`
	WorkerStart        string         `json:"workerStart"`
	BootID             string         `json:"bootId"`
	ExecutableIdentity string         `json:"executableIdentity"`
	StopIdentity       string         `json:"stopIdentity,omitempty"`
	PID                int            `json:"pid"`
	ProcessStart       string         `json:"processStart"`
	Output             string         `json:"output"`
	Truncated          bool           `json:"truncated"`
	ExitCode           int            `json:"exitCode"`
	Message            string         `json:"message"`
}

func (run ManagedRun) Validate() error {
	if run.ID.Validate() != nil || run.ProjectID.Validate() != nil || validateOptionalID(run.WorkspaceID) != nil || validateOptionalID(run.LaunchID) != nil {
		return errors.New("invalid runtime identity")
	}
	if err := run.Command.Validate(); err != nil {
		return err
	}
	if run.Command.Approval == nil {
		return errors.New("managed execution requires approval")
	}
	if err := validateTimes(run.StartedAt, run.UpdatedAt); err != nil {
		return err
	}
	switch run.State {
	case "pending", "starting", "running", "ready", "failed", "stopping", "stopped", "unknown":
	default:
		return errors.New("invalid runtime state")
	}
	if run.WorkerPID < 0 || run.PID < 0 || len(run.ExecutableIdentity) > 128 || len(run.WorkerStart) > 64 || len(run.ProcessStart) > 64 || len(run.BootID) > 64 || len(run.Output) > 128<<10 || len(run.Message) > 4096 {
		return errors.New("invalid runtime data")
	}
	return nil
}
