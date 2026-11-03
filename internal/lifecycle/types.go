package lifecycle

import (
	"context"
	"time"

	"workspace-manager/internal/applications"
	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

type Request struct {
	Action          string                  `json:"action"`
	ID              domain.ID               `json:"id,omitempty"`
	Token           string                  `json:"token,omitempty"`
	Name            string                  `json:"name,omitempty"`
	Revision        storage.Revision        `json:"revision,omitempty"`
	HistoryRevision storage.Revision        `json:"historyRevision,omitempty"`
	SwitchPolicy    string                  `json:"switchPolicy,omitempty"`
	Force           bool                    `json:"force,omitempty"`
	Save            bool                    `json:"save,omitempty"`
	Settings        *domain.Settings        `json:"settings,omitempty"`
	WorkspaceLaunch *domain.WorkspaceLaunch `json:"workspaceLaunch,omitempty"`
}
type Response struct {
	Code               string                         `json:"code"`
	Message            string                         `json:"message"`
	Plan               *Plan                          `json:"plan,omitempty"`
	Runtime            *Runtime                       `json:"runtime,omitempty"`
	Sessions           []SessionView                  `json:"sessions,omitempty"`
	Session            *SessionView                   `json:"session,omitempty"`
	Projects           []domain.Project               `json:"projects,omitempty"`
	Workspaces         []domain.Workspace             `json:"workspaces,omitempty"`
	WorkspaceRevisions map[domain.ID]storage.Revision `json:"workspaceRevisions,omitempty"`
	Settings           *domain.Settings               `json:"settings,omitempty"`
	SettingsRevision   storage.Revision               `json:"settingsRevision,omitempty"`
	History            []storage.Revision             `json:"history,omitempty"`
}
type SessionView struct {
	Value    domain.Session   `json:"value"`
	Revision storage.Revision `json:"revision"`
}
type TaskPlan struct {
	Key          string                       `json:"key"`
	Kind         string                       `json:"kind"`
	Name         string                       `json:"name"`
	ProjectID    domain.ID                    `json:"projectId,omitempty"`
	Command      *domain.StartupCommand       `json:"command,omitempty"`
	Action       *domain.CommandAction        `json:"action,omitempty"`
	Application  *domain.WorkspaceApplication `json:"application,omitempty"`
	DesktopEntry *applications.DesktopEntry   `json:"desktopEntry,omitempty"`
	URL          string                       `json:"url,omitempty"`
	Identity     string                       `json:"identity"`
	StopIdentity string                       `json:"stopIdentity,omitempty"`
	DelaySeconds int                          `json:"delaySeconds"`
	Blocked      string                       `json:"blocked,omitempty"`
}
type Plan struct {
	Token        string                        `json:"token"`
	Fingerprint  string                        `json:"fingerprint"`
	Kind         string                        `json:"kind"`
	ID           domain.ID                     `json:"id"`
	Name         string                        `json:"name"`
	WorkspaceID  domain.ID                     `json:"workspaceId,omitempty"`
	SessionID    domain.ID                     `json:"sessionId,omitempty"`
	Tasks        []TaskPlan                    `json:"tasks"`
	Warnings     []string                      `json:"warnings"`
	Projects     []domain.Project              `json:"projects,omitempty"`
	Applications []domain.WorkspaceApplication `json:"applications,omitempty"`
	URLs         []string                      `json:"urls,omitempty"`
	ExpiresAt    time.Time                     `json:"expiresAt"`
}
type TaskStatus struct {
	Key     string    `json:"key"`
	Name    string    `json:"name"`
	State   string    `json:"state"`
	Message string    `json:"message"`
	RunID   domain.ID `json:"runId,omitempty"`
}
type Runtime struct {
	ActiveWorkspaceID domain.ID           `json:"activeWorkspaceId"`
	SessionOrigin     domain.ID           `json:"sessionOrigin"`
	LaunchID          domain.ID           `json:"launchId"`
	Busy              bool                `json:"busy"`
	Tasks             []TaskStatus        `json:"tasks"`
	Runs              []domain.ManagedRun `json:"runs"`
	ShutdownState     string              `json:"shutdownState"`
	Message           string              `json:"message"`
}

// Desktop hooks are implemented in the Linux adapter, not in the task engine.
// Reviews return fingerprints of current files and identities; dispatch rechecks.
type Hooks struct {
	ReviewAction           func(context.Context, domain.CommandAction) (string, error)
	LaunchAction           func(context.Context, domain.CommandAction) error
	ReviewApplication      func(context.Context, domain.WorkspaceApplication) (string, error)
	DescribeApplication    func(context.Context, domain.WorkspaceApplication) *applications.DesktopEntry
	LaunchApplication      func(context.Context, domain.WorkspaceApplication) error
	RecognizedApplications func(context.Context) ([]domain.WorkspaceApplication, []string)
	OpenURL                func(context.Context, string) error
	Shutdown               func(context.Context) error
}

func failure(message string) Response { return Response{Code: "failed", Message: message} }
