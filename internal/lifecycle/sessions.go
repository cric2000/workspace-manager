package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

func (c *Controller) capture(ctx context.Context, name string, parent domain.ID) (domain.Session, error) {
	id, err := domain.NewID()
	if err != nil {
		return domain.Session{}, err
	}
	session := domain.Session{ID: id, Name: cleanName(name), CapturedAt: time.Now().UTC(), WorkspaceID: c.runtime.ActiveWorkspaceID, ParentSessionID: parent, Projects: []domain.Project{}, Applications: domain.CloneApplications(c.activeApplications), URLs: slices.Clone(c.activeURLs), CaptureWarnings: []string{"Capture contains supported launch metadata only. Unsaved documents, terminal history and arbitrary browser tabs cannot be recovered.", "Project source and environment file contents are not copied into snapshots."}}
	for _, p := range c.activeProjects {
		clone := p
		clone.Commands = domain.CloneCommands(p.Commands)
		clone.Preferences = domain.ClonePreferences(p.Preferences)
		session.Projects = append(session.Projects, clone)
	}
	runs, err := c.runs.ListBounded(ctx, 256)
	if err != nil {
		return session, err
	}
	for _, snapshot := range runs {
		r := snapshot.Value
		if terminal(r.State) {
			continue
		}
		index := slices.IndexFunc(session.Projects, func(p domain.Project) bool { return p.ID == r.ProjectID })
		if index < 0 {
			p, err := c.projects.Get(ctx, r.ProjectID)
			if err != nil {
				session.CaptureWarnings = append(session.CaptureWarnings, "A managed project registration is unavailable: "+string(r.ProjectID))
				continue
			}
			value := p.Value
			value.Commands = []domain.StartupCommand{}
			session.Projects = append(session.Projects, value)
			index = len(session.Projects) - 1
		}
		if !slices.ContainsFunc(session.Projects[index].Commands, func(cmd domain.StartupCommand) bool { return cmd.ID == r.Command.ID }) {
			session.Projects[index].Commands = append(session.Projects[index].Commands, domain.CloneCommands([]domain.StartupCommand{r.Command})[0])
		}
		if r.State == "unknown" || r.BootID != bootID() || processStart(r.WorkerPID) != r.WorkerStart {
			session.CaptureWarnings = append(session.CaptureWarnings, "A worker has an unknown outcome. Restore will refuse duplicate command execution until it is reconciled.")
		}
	}
	if c.hooks.RecognizedApplications != nil {
		apps, warnings := c.hooks.RecognizedApplications(ctx)
		session.CaptureWarnings = append(session.CaptureWarnings, warnings...)
		for _, a := range apps {
			if !slices.ContainsFunc(session.Applications, func(v domain.WorkspaceApplication) bool { return applicationKey(v) == applicationKey(a) }) {
				session.Applications = append(session.Applications, a)
			}
		}
	}
	if err := session.Validate(); err != nil {
		return session, err
	}
	return session, nil
}
func applicationKey(app domain.WorkspaceApplication) string {
	if app.Custom != nil {
		return "custom:" + string(app.Custom.ID)
	}
	return "desktop:" + string(app.DesktopID)
}
func (c *Controller) persistSnapshot(ctx context.Context, session domain.Session) error {
	values, err := c.sessions.ListBounded(ctx, 256)
	if err != nil {
		return err
	}
	if len(values) >= 256 {
		return errors.New("session limit reached; delete unneeded snapshots explicitly")
	}
	if err := c.sessions.Create(ctx, session); err != nil {
		return err
	}
	saved, err := c.sessions.Get(ctx, session.ID)
	if err != nil {
		return err
	}
	a, _ := json.Marshal(session)
	b, _ := json.Marshal(saved.Value)
	if string(a) != string(b) {
		return errors.New("snapshot verification failed")
	}
	return nil
}
func (c *Controller) sessionAction(ctx context.Context, req Request) Response {
	switch req.Action {
	case "capture":
		session, err := c.capture(ctx, req.Name, c.runtime.SessionOrigin)
		if err != nil {
			return failure("Session capture failed. Nothing was stopped or shut down.")
		}
		if err = c.persistSnapshot(ctx, session); err != nil {
			return failure("Snapshot could not be saved and verified. Existing snapshots are preserved; reload before retrying.")
		}
		saved, _ := c.sessions.Get(ctx, session.ID)
		return Response{Message: "Session saved and verified.", Session: &SessionView{saved.Value, saved.Revision}}
	case "branch":
		old, err := c.sessions.Get(ctx, req.ID)
		if err != nil {
			return failure("Source session is unavailable.")
		}
		var session domain.Session
		if req.Save {
			session, err = c.capture(ctx, req.Name, req.ID)
		} else {
			session, err = old.Value.Branch(cleanName(req.Name), time.Now().UTC())
		}
		if err != nil {
			return failure("Use a valid name and available source session.")
		}
		if err = c.persistSnapshot(ctx, session); err != nil {
			return failure("Branch could not be saved and verified. The original snapshot is preserved.")
		}
		saved, _ := c.sessions.Get(ctx, session.ID)
		return Response{Message: "Independent session branch saved. The original is unchanged.", Session: &SessionView{saved.Value, saved.Revision}}
	case "rename_session":
		err := storage.ReviseSession(ctx, c.sessions, req.ID, req.Revision, func(s domain.Session) (domain.Session, error) { s.Name = cleanName(req.Name); return s, s.Validate() })
		if err != nil {
			return failure("Session changed or its new name is invalid. Reload before editing again.")
		}
		return Response{Message: "Session renamed. Its previous revision remains recoverable."}
	case "update_session":
		old, err := c.sessions.Get(ctx, req.ID)
		if err != nil || old.Revision != req.Revision {
			return failure("Session changed. Reload before explicitly updating it.")
		}
		session, err := c.capture(ctx, old.Value.Name, old.Value.ParentSessionID)
		if err != nil {
			return failure("Capture failed; the old snapshot was preserved.")
		}
		session.ID = old.Value.ID
		err = storage.ReviseSession(ctx, c.sessions, req.ID, req.Revision, func(domain.Session) (domain.Session, error) { return session, nil })
		if err != nil {
			return failure("Snapshot update failed. Reload its recoverable history before retrying.")
		}
		return Response{Message: "Session explicitly updated. Its previous revision remains recoverable."}
	case "delete_session":
		if req.ID == c.runtime.SessionOrigin {
			return failure("This snapshot is the active session origin. Launch a workspace or restore another session before deleting it.")
		}
		// Keep ancestry and selected restore targets valid. No implicit cascade.
		settings, err := c.getSettings(ctx)
		if err != nil {
			return failure("Settings unavailable; deletion refused.")
		}
		if settings.Value.RestoreSessionID == req.ID {
			return failure("Choose another restore session in Settings before deleting this snapshot.")
		}
		sessions, err := c.sessions.ListBounded(ctx, 256)
		if err != nil {
			return failure("Session ancestry cannot be checked.")
		}
		for _, s := range sessions {
			if s.Value.ParentSessionID == req.ID {
				return failure("This snapshot has branches. Delete those explicitly before their parent.")
			}
		}
		if err := c.sessions.Delete(ctx, req.ID, req.Revision); err != nil {
			return failure("Session changed or deletion failed. Reload before retrying.")
		}
		return Response{Message: "Session removed. Project directories are preserved."}
	case "session_history":
		revisions, err := c.sessions.History(ctx, req.ID)
		if err != nil {
			return failure("Session history cannot be read.")
		}
		return Response{History: revisions}
	case "restore_revision":
		if err := c.sessions.RestoreRevision(ctx, req.ID, req.Revision, req.HistoryRevision); err != nil {
			return failure("Revision restore failed or the session changed. Reload before trying again.")
		}
		return Response{Message: "Session revision restored. The replaced revision remains recoverable."}
	}
	return failure("Unsupported session operation.")
}
