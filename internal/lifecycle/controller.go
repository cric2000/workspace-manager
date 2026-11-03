package lifecycle

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

type heldPlan struct {
	plan          Plan
	request       Request
	started       bool
	loginDispatch bool
	result        Response
}
type Controller struct {
	mu                 sync.Mutex
	paths              storage.Paths
	hooks              Hooks
	executable         string
	profiles           *storage.Repository[domain.Workspace]
	projects           *storage.Repository[domain.Project]
	sessions           *storage.Repository[domain.Session]
	runs               *storage.Repository[domain.ManagedRun]
	grants             *storage.Repository[domain.StartupAuthorization]
	selection          *storage.Repository[domain.ManagedSelection]
	settings           *storage.SettingsRepository
	operations         *os.Root
	plans              map[string]*heldPlan
	runtime            Runtime
	cancel             context.CancelFunc
	activeProjects     []domain.Project
	activeApplications []domain.WorkspaceApplication
	activeURLs         []string
	closed             bool
	workers            sync.WaitGroup
	life               context.Context
	stop               context.CancelFunc
}

func NewController(paths storage.Paths, hooks Hooks, executable string) (*Controller, error) {
	c := &Controller{paths: paths, hooks: hooks, executable: executable, plans: map[string]*heldPlan{}, runtime: Runtime{ShutdownState: "idle", Runs: []domain.ManagedRun{}, Tasks: []TaskStatus{}}}
	c.life, c.stop = context.WithCancel(context.Background())
	wc, _ := storage.NewWorkspaceCodec()
	pc, _ := storage.NewProjectCodec()
	sc, _ := storage.NewSessionCodec()
	tc, _ := storage.NewCodec[domain.Settings](1, nil)
	ac, _ := storage.NewCodec[domain.StartupAuthorization](1, nil)
	var err error
	if c.profiles, err = storage.OpenRepository(paths.WorkspacesDir(), wc); err != nil {
		return nil, err
	}
	if c.projects, err = storage.OpenRepository(paths.ProjectsDir(), pc); err != nil {
		c.Close()
		return nil, err
	}
	if c.sessions, err = storage.OpenRepository(paths.SessionsDir(), sc); err != nil {
		c.Close()
		return nil, err
	}
	if c.runs, err = openRuns(paths); err != nil {
		c.Close()
		return nil, err
	}
	if c.grants, err = storage.OpenRepository(filepath.Join(paths.StateDir, "startup-authorizations"), ac); err != nil {
		c.Close()
		return nil, err
	}
	if c.settings, err = storage.OpenSettingsRepository(paths.ConfigDir, tc); err != nil {
		c.Close()
		return nil, err
	}
	if c.operations, err = storage.OpenPrivateDirectory(paths.WorkspaceOperationsDir()); err != nil {
		c.Close()
		return nil, err
	}
	selectionCodec, _ := storage.NewCodec[domain.ManagedSelection](1, nil)
	if c.selection, err = storage.OpenRepository(filepath.Join(runtimeDirectory(paths), "selection"), selectionCodec); err != nil {
		c.Close()
		return nil, err
	}
	priorRuns, e := c.runs.ListBounded(context.Background(), 256)
	if e != nil {
		c.Close()
		return nil, e
	}
	for _, snapshot := range priorRuns {
		r := snapshot.Value
		if r.BootID != "" && r.BootID != bootID() && !terminal(r.State) {
			r.State = "stopped"
			r.Message = "Previous boot ended. No process from that boot can still be running."
			r.UpdatedAt = time.Now().UTC()
			if r.UpdatedAt.Before(snapshot.Value.UpdatedAt) {
				r.UpdatedAt = snapshot.Value.UpdatedAt
			}
			if e := storage.UpdateRuntime(context.Background(), c.runs, r, snapshot.Revision); e != nil {
				c.Close()
				return nil, e
			}
		}
	}
	if selected, e := c.selection.Get(context.Background(), selectionID); e == nil {
		c.runtime.ActiveWorkspaceID = selected.Value.Snapshot.WorkspaceID
		c.runtime.SessionOrigin = selected.Value.OriginSessionID
		c.activeProjects = selected.Value.Snapshot.Projects
		c.activeApplications = selected.Value.Snapshot.Applications
		c.activeURLs = selected.Value.Snapshot.URLs
		c.runtime.Message = "Recovered known launch metadata. Worker state is verified independently."
	} else if !os.IsNotExist(e) {
		c.Close()
		return nil, e
	}
	return c, nil
}
func (c *Controller) Close() {
	c.mu.Lock()
	c.closed = true
	c.stop()
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()
	c.workers.Wait()
	if c.profiles != nil {
		c.profiles.Close()
	}
	if c.projects != nil {
		c.projects.Close()
	}
	if c.sessions != nil {
		c.sessions.Close()
	}
	if c.runs != nil {
		c.runs.Close()
	}
	if c.grants != nil {
		c.grants.Close()
	}
	if c.settings != nil {
		c.settings.Close()
	}
	if c.operations != nil {
		c.operations.Close()
	}
	if c.selection != nil {
		c.selection.Close()
	}
}

// Handle serializes controller mutations. Long launch/stop work runs separately;
// GUI cancellation cannot silently retract an already accepted dispatch.
func (c *Controller) Handle(ctx context.Context, req Request) Response {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || ctx.Err() != nil {
		return failure("The request was cancelled or the controller is closing.")
	}
	switch req.Action {
	case "state":
		return c.state(ctx)
	case "review_project", "review_workspace", "review_session", "review_login", "review_shutdown":
		return c.review(ctx, req)
	case "commit":
		return c.commit(ctx, req)
	case "outcome":
		if p := c.plans[req.Token]; p != nil {
			if p.started {
				return p.result
			}
			return Response{Code: "pending", Message: "Review has not been committed."}
		}
		return failure("Review is unavailable. Inspect Activity before creating another review.")
	case "cancel_launch":
		if c.cancel != nil {
			c.cancel()
		}
		return Response{Message: "Cancellation requested. Already running services are preserved; stop them explicitly."}
	case "stop_controller":
		if c.runtime.Busy {
			return failure("Cancel or finish the current operation before stopping the controller.")
		}
		return Response{Message: "Controller disconnect requested. Independent managed workers are preserved."}
	case "stop":
		return c.stopRun(ctx, req)
	case "capture", "branch", "rename_session", "update_session", "delete_session", "session_history", "restore_revision":
		return c.sessionAction(ctx, req)
	case "save_settings":
		return c.saveSettings(ctx, req)
	case "configure_workspace":
		return c.configureWorkspace(ctx, req)
	case "clear_finished":
		return c.clearFinished(ctx)
	case "reconcile_run":
		return c.reconcileRun(ctx, req)
	case "login":
		return c.login(ctx)
	default:
		return failure("Unknown controller operation.")
	}
}

func (c *Controller) getSettings(ctx context.Context) (storage.Snapshot[domain.Settings], error) {
	value, err := c.settings.Get(ctx)
	if errors.Is(err, fs.ErrNotExist) {
		err = c.settings.Create(ctx, domain.DefaultSettings())
		if err == nil || errors.Is(err, fs.ErrExist) {
			value, err = c.settings.Get(ctx)
		}
	}
	return value, err
}
func (c *Controller) state(ctx context.Context) Response {
	settings, err := c.getSettings(ctx)
	if err != nil {
		return failure("Settings cannot be read. Repair or restore their history before continuing.")
	}
	profiles, err := c.profiles.ListBounded(ctx, 256)
	if err != nil {
		return failure("Workspace configuration is unavailable.")
	}
	projects, err := c.projects.ListBounded(ctx, 256)
	if err != nil {
		return failure("Project registry is unavailable.")
	}
	sessions, err := c.sessions.ListBounded(ctx, 256)
	if err != nil {
		return failure("Session storage is unavailable or exceeds 256 snapshots. Existing snapshots are preserved.")
	}
	runs, err := c.runs.ListBounded(ctx, 256)
	if err != nil {
		return failure("Runtime history is unavailable. No process was adopted or started.")
	}
	result := Response{Settings: &settings.Value, SettingsRevision: settings.Revision, Projects: []domain.Project{}, Workspaces: []domain.Workspace{}, WorkspaceRevisions: map[domain.ID]storage.Revision{}, Sessions: []SessionView{}}
	rt := c.runtime
	rt.Tasks = slices.Clone(rt.Tasks)
	rt.Runs = []domain.ManagedRun{}
	for _, snapshot := range runs {
		run := snapshot.Value
		if !terminal(run.State) && run.WorkerPID > 0 && (run.BootID != bootID() || processStart(run.WorkerPID) != run.WorkerStart) {
			run.State = "unknown"
			run.Message = "Worker is unavailable. Inspect any remaining descendants before retrying. No PID was adopted."
		}
		rt.Runs = append(rt.Runs, run)
	}
	slices.SortFunc(rt.Runs, func(a, b domain.ManagedRun) int { return b.StartedAt.Compare(a.StartedAt) })
	for index, task := range rt.Tasks {
		for _, run := range rt.Runs {
			if task.RunID == run.ID {
				rt.Tasks[index].State = run.State
				if run.Message != "" {
					rt.Tasks[index].Message = run.Message
				}
				break
			}
		}
	}
	for _, v := range profiles {
		result.Workspaces = append(result.Workspaces, v.Value)
		result.WorkspaceRevisions[v.Value.ID] = v.Revision
	}
	for _, v := range projects {
		result.Projects = append(result.Projects, v.Value)
	}
	for _, v := range sessions {
		result.Sessions = append(result.Sessions, SessionView{v.Value, v.Revision})
	}
	slices.SortFunc(result.Sessions, func(a, b SessionView) int { return b.Value.CapturedAt.Compare(a.Value.CapturedAt) })
	result.Runtime = &rt
	return result
}
func terminal(state string) bool { return state == "stopped" }

func (c *Controller) buildPlan(ctx context.Context, req Request) (Plan, error) {
	plan := Plan{Kind: req.Action, ID: req.ID, Tasks: []TaskPlan{}, Warnings: []string{}}
	var projects []domain.Project
	var apps []domain.WorkspaceApplication
	var urls []string
	var launch *domain.WorkspaceLaunch
	addWorkspace := func(id domain.ID) error {
		snapshot, err := c.profiles.Get(ctx, id)
		if err != nil {
			return err
		}
		w := snapshot.Value
		plan.WorkspaceID = w.ID
		plan.Name = w.Name
		apps = append(apps, domain.CloneApplications(w.Applications)...)
		launch = w.Launch
		for _, id := range w.ProjectIDs {
			p, err := c.projects.Get(ctx, id)
			if err != nil {
				plan.Warnings = append(plan.Warnings, "A referenced project is unavailable: "+string(id))
				continue
			}
			projects = append(projects, p.Value)
		}
		if w.Launch != nil {
			urls = append(urls, w.Launch.URLs...)
		}
		return nil
	}
	addSession := func(id domain.ID) error {
		snapshot, err := c.sessions.Get(ctx, id)
		if err != nil {
			return err
		}
		s := snapshot.Value
		plan.SessionID = s.ID
		plan.WorkspaceID = s.WorkspaceID
		plan.Name = s.Name
		projects = append(projects, s.Projects...)
		apps = append(apps, domain.CloneApplications(s.Applications)...)
		urls = append(urls, s.URLs...)
		plan.Warnings = append(plan.Warnings, s.CaptureWarnings...)
		return nil
	}
	switch req.Action {
	case "review_project":
		snapshot, err := c.projects.Get(ctx, req.ID)
		if err != nil {
			return plan, err
		}
		projects = []domain.Project{snapshot.Value}
		plan.Name = snapshot.Value.Name
	case "review_workspace":
		if err := addWorkspace(req.ID); err != nil {
			return plan, err
		}
	case "review_session":
		if err := addSession(req.ID); err != nil {
			return plan, err
		}
	case "review_login":
		settings := req.Settings
		if settings == nil {
			snapshot, err := c.getSettings(ctx)
			if err != nil {
				return plan, err
			}
			settings = &snapshot.Value
		}
		if err := settings.Validate(); err != nil {
			return plan, err
		}
		plan.ID = settings.DefaultWorkspaceID
		if settings.StartupMode != domain.StartupFixed {
			if err := addSession(settings.RestoreSessionID); err != nil {
				return plan, err
			}
		}
		if settings.StartupMode != domain.StartupRestore {
			if err := addWorkspace(settings.DefaultWorkspaceID); err != nil {
				return plan, err
			}
		}
		plan.Name = "Login startup · " + string(settings.StartupMode)
	case "review_shutdown":
		plan.Name = "Save & Shutdown"
		plan.WorkspaceID = c.runtime.ActiveWorkspaceID
		plan.SessionID = c.runtime.SessionOrigin
		plan.Warnings = []string{"Only supported application metadata and managed project configuration can be restored.", "Unsaved documents, terminal scrollback and arbitrary browser tabs are not captured.", "Managed services will be stopped gracefully. Other applications retain their normal save dialogs.", "Completion means the normal shutdown request was handed to the desktop, not that the computer powered off."}
		runs, err := c.runs.ListBounded(ctx, 256)
		if err != nil {
			return plan, err
		}
		for _, r := range runs {
			if !terminal(r.Value.State) {
				plan.Tasks = append(plan.Tasks, TaskPlan{Key: string(r.Value.ID), Kind: "stop", Name: r.Value.Command.Configuration.Name, Identity: r.Value.Command.Configuration.Fingerprint() + r.Value.WorkerStart})
			}
		}
	default:
		return plan, errors.New("invalid review")
	}
	seen := map[string]bool{}
	uniqueProjects := make([]domain.Project, 0, len(projects))
	projectPaths := map[string]bool{}
	for _, p := range projects {
		path := p.Path
		if canonical, err := filepath.EvalSymlinks(path); err == nil {
			path = canonical
		}
		if projectPaths[path] {
			plan.Warnings = append(plan.Warnings, "Duplicate project directory omitted; the first reviewed configuration takes priority.")
			continue
		}
		projectPaths[path] = true
		uniqueProjects = append(uniqueProjects, p)
	}
	projects = uniqueProjects
	add := func(task TaskPlan) {
		if seen[task.Key] {
			return
		}
		seen[task.Key] = true
		plan.Tasks = append(plan.Tasks, task)
	}
	delay := 0
	if launch != nil {
		delay = launch.DelaySeconds
	}
	for _, app := range apps {
		a := app
		key := "desktop:" + string(a.DesktopID)
		if a.Custom != nil {
			key = "custom:" + string(a.Custom.ID)
		}
		identity, err := c.hooks.ReviewApplication(ctx, a)
		task := TaskPlan{Key: key, Kind: "application", Name: string(a.DesktopID), Application: &a, Identity: identity, DelaySeconds: delay}
		if c.hooks.DescribeApplication != nil {
			task.DesktopEntry = c.hooks.DescribeApplication(ctx, a)
			if task.DesktopEntry != nil {
				task.Name = task.DesktopEntry.Name
			}
		}
		if a.Custom != nil {
			task.Name = a.Custom.Name
			task.Action = &domain.CommandAction{Executable: a.Custom.Executable, Arguments: a.Custom.Arguments, WorkingDirectory: a.Custom.WorkingDirectory}
		}
		if err != nil {
			task.Blocked = "Application is unavailable; launch will be skipped."
		}
		add(task)
	}
	for _, project := range projects {
		p := project
		available := false
		if info, err := os.Stat(p.Path); err == nil && info.IsDir() {
			available = true
		}
		preferences := p.Preferences
		if preferences != nil && preferences.Approval != nil && preferences.Editor != nil {
			action := domain.CloneAction(preferences.Editor)
			identity, err := c.hooks.ReviewAction(ctx, *action)
			task := TaskPlan{Key: "editor:" + string(p.ID), Kind: "editor", Name: "Open " + p.Name + " in editor", ProjectID: p.ID, Action: action, Identity: identity, DelaySeconds: delay}
			if err != nil || !available {
				task.Blocked = "Editor or project directory is unavailable."
			}
			add(task)
		} else if launch != nil && launch.DefaultEditor != nil {
			action := domain.CloneAction(launch.DefaultEditor)
			action.Arguments = append(action.Arguments, p.Path)
			action.WorkingDirectory = p.Path
			identity, err := c.hooks.ReviewAction(ctx, *action)
			task := TaskPlan{Key: "editor:" + string(p.ID), Kind: "editor", Name: "Open " + p.Name + " in default editor", ProjectID: p.ID, Action: action, Identity: identity, DelaySeconds: delay}
			if err != nil || !available {
				task.Blocked = "Default editor or project directory is unavailable."
			}
			add(task)
		}
		for _, command := range domain.CloneCommands(p.Commands) {
			cmd := command
			configuration := cmd.Configuration
			action := domain.CommandAction{Executable: configuration.Executable, Arguments: configuration.Arguments, WorkingDirectory: configuration.WorkingDirectory}
			task := TaskPlan{Key: "command:" + executionIdentity(configuration), Kind: "command", Name: p.Name + " · " + configuration.Name, ProjectID: p.ID, Command: &cmd, Action: &action, DelaySeconds: delay}
			if cmd.Approval == nil {
				task.Blocked = "Startup command is unapproved. Review it in project configuration first."
			} else {
				identity, err := c.hooks.ReviewAction(ctx, action)
				task.Identity = identity
				if err != nil || !available {
					task.Blocked = "Executable or working directory is unavailable."
				}
			}
			if configuration.Runtime != nil && configuration.Runtime.Stop != nil && configuration.Runtime.Stop.Action != nil {
				identity, err := c.hooks.ReviewAction(ctx, *configuration.Runtime.Stop.Action)
				task.StopIdentity = identity
				if err != nil {
					task.Blocked = "Configured stop executable or working directory is unavailable."
				}
			}
			add(task)
		}
		if preferences != nil && preferences.Approval != nil && preferences.PreviewURL != "" {
			add(TaskPlan{Key: "preview:" + string(p.ID), Kind: "preview", Name: "Preview " + p.Name, ProjectID: p.ID, URL: preferences.PreviewURL, DelaySeconds: delay})
		}
	}
	for _, url := range urls {
		add(TaskPlan{Key: "url:" + url, Kind: "url", Name: url, URL: url, DelaySeconds: delay})
	}
	for _, p := range projects {
		if !slices.ContainsFunc(plan.Projects, func(value domain.Project) bool { return value.ID == p.ID }) {
			plan.Projects = append(plan.Projects, p)
		}
	}
	for _, a := range apps {
		if !slices.ContainsFunc(plan.Applications, func(value domain.WorkspaceApplication) bool { return applicationKey(value) == applicationKey(a) }) {
			plan.Applications = append(plan.Applications, a)
		}
	}
	for _, url := range urls {
		if !slices.Contains(plan.URLs, url) {
			plan.URLs = append(plan.URLs, url)
		}
	}
	// Settings are included so an autostart grant cannot change its startup mode,
	// target or delay without another explicit review.
	payload, _ := json.Marshal(struct {
		Plan     Plan
		Settings *domain.Settings
	}{plan, req.Settings})
	if len(plan.Tasks) > 256 || len(payload) > storage.MaxDocumentBytes {
		return plan, errors.New("launch review exceeds bounded task limits")
	}
	hash := sha256.Sum256(payload)
	plan.Fingerprint = hex.EncodeToString(hash[:])
	return plan, nil
}
func (c *Controller) review(ctx context.Context, req Request) Response {
	if req.Action == "review_login" && req.Settings != nil {
		copy := *req.Settings
		req.Settings = &copy
	}
	plan, err := c.buildPlan(ctx, req)
	if err != nil {
		return failure("Could not review the selected configuration. Check project paths, applications and settings.")
	}
	now := time.Now()
	for token, p := range c.plans {
		if now.After(p.plan.ExpiresAt) && !c.runtime.Busy {
			delete(c.plans, token)
		}
	}
	if len(c.plans) >= 64 {
		return failure("Too many recent reviews. Wait five minutes before another review.")
	}
	plan.Token = rand.Text()
	plan.ExpiresAt = now.Add(5 * time.Minute)
	data, _ := json.Marshal(plan)
	var held Plan
	_ = json.Unmarshal(data, &held)
	c.plans[plan.Token] = &heldPlan{plan: held, request: req}
	return Response{Plan: &plan}
}

func (c *Controller) commit(ctx context.Context, req Request) Response {
	held := c.plans[req.Token]
	if held == nil {
		return failure("Review is unavailable. Inspect Activity before reviewing again.")
	}
	if held.started {
		return held.result
	}
	if time.Now().After(held.plan.ExpiresAt) {
		return failure("Review expired. Review the current configuration again.")
	}
	if c.runtime.Busy {
		return failure("An operation is already active. Cancel it or wait for completion.")
	}
	current, err := c.buildPlan(ctx, held.request)
	if err != nil || current.Fingerprint != held.plan.Fingerprint {
		return failure("Configuration or executable changed after review. Review again.")
	}
	if held.plan.Kind == "review_login" && !held.loginDispatch {
		return c.enableLogin(ctx, held)
	}
	if held.plan.Kind == "review_shutdown" {
		return c.beginShutdown(ctx, held, req)
	}
	if req.SwitchPolicy == "cancel" {
		return Response{Code: "cancelled", Message: "Workspace switch cancelled."}
	}
	if req.SwitchPolicy != "keep" && req.SwitchPolicy != "stop_previous" {
		return failure("Choose whether to keep current services or stop services belonging only to the previous workspace.")
	}
	if err := c.saveSelection(ctx, held.plan); err != nil {
		return failure("Known launch metadata could not be saved. No tasks were dispatched.")
	}
	held.started = true
	held.result = Response{Message: "Launch accepted. Inspect Activity for progress; repeating this review never launches twice."}
	launchCtx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.runtime.Busy = true
	c.runtime.Message = "Launching " + held.plan.Name
	c.runtime.Tasks = []TaskStatus{}
	c.runtime.LaunchID, _ = domain.NewID()
	previous := c.runtime.ActiveWorkspaceID
	c.runtime.ActiveWorkspaceID = held.plan.WorkspaceID
	c.runtime.SessionOrigin = held.plan.SessionID
	c.activeProjects = held.plan.Projects
	c.activeApplications = domain.CloneApplications(held.plan.Applications)
	c.activeURLs = slices.Clone(held.plan.URLs)
	plan := held.plan
	launchID := c.runtime.LaunchID
	c.workers.Add(1)
	go func() { defer c.workers.Done(); c.executePlan(launchCtx, plan, launchID, previous, req.SwitchPolicy) }()
	return held.result
}

func (c *Controller) startRun(ctx context.Context, task TaskPlan, plan Plan, launchID domain.ID) (domain.ID, error) {
	values, err := c.runs.ListBounded(ctx, 256)
	if err != nil {
		return "", err
	}
	if len(values) >= 256 {
		return "", errors.New("runtime history is full; clear finished entries in Activity")
	}
	for _, snapshot := range values {
		v := snapshot.Value
		if (v.ProjectID == task.ProjectID && v.Command.ID == task.Command.ID || executionIdentity(v.Command.Configuration) == executionIdentity(task.Command.Configuration)) && !terminal(v.State) {
			if v.Command.Configuration.Fingerprint() == task.Command.Configuration.Fingerprint() && v.ExecutableIdentity == task.Identity && v.BootID == bootID() && v.WorkerStart != "" && processStart(v.WorkerPID) == v.WorkerStart {
				return v.ID, nil
			}
			return v.ID, errors.New("a previous attempt still owns or may own this command; inspect Activity")
		}
	}
	id, err := domain.NewID()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	run := domain.ManagedRun{ID: id, ProjectID: task.ProjectID, Command: *task.Command, WorkspaceID: plan.WorkspaceID, LaunchID: launchID, StartedAt: now, UpdatedAt: now, State: "pending", ExecutableIdentity: task.Identity, StopIdentity: task.StopIdentity, BootID: bootID()}
	if err := c.runs.Create(ctx, run); err != nil {
		return "", err
	}
	command := exec.Command(c.executable, "--task-worker", string(id))
	command.Env = os.Environ()
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return id, err
	}
	go func() { _ = command.Wait() }()
	return id, nil
}
func (c *Controller) setTask(index int, state, message string, id domain.ID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if index < len(c.runtime.Tasks) {
		c.runtime.Tasks[index].State = state
		c.runtime.Tasks[index].Message = message
		c.runtime.Tasks[index].RunID = id
	}
}

func (c *Controller) executePlan(ctx context.Context, plan Plan, launchID, previous domain.ID, policy string) {
	message := "Launch finished. Review each task result."
	defer func() {
		c.mu.Lock()
		c.runtime.Busy = false
		c.runtime.Message = message
		c.cancel = nil
		c.mu.Unlock()
	}()
	c.mu.Lock()
	for _, task := range plan.Tasks {
		c.runtime.Tasks = append(c.runtime.Tasks, TaskStatus{Key: task.Key, Name: task.Name, State: "pending"})
	}
	c.mu.Unlock()
	if policy == "stop_previous" && previous != "" && previous != plan.WorkspaceID {
		var stopping []domain.ID
		required := map[domain.ID]bool{}
		requiredExecutions := map[string]bool{}
		for _, task := range plan.Tasks {
			required[task.ProjectID] = true
			if task.Command != nil {
				requiredExecutions[executionIdentity(task.Command.Configuration)] = true
			}
		}
		runs, err := c.runs.ListBounded(ctx, 256)
		if err != nil {
			message = "Switch stopped: runtime history could not be read. No new tasks were dispatched."
			return
		}
		for _, snapshot := range runs {
			r := snapshot.Value
			if r.WorkspaceID == previous && !required[r.ProjectID] && !requiredExecutions[executionIdentity(r.Command.Configuration)] && !terminal(r.State) {
				if err := signalWorker(r, false); err != nil {
					message = "Switch stopped: a previous service could not be stopped safely. No new tasks were dispatched."
					return
				}
				stopping = append(stopping, r.ID)
			}
		}
		if err := c.waitStopped(ctx, stopping); err != nil {
			message = "Switch stopped: previous services did not stop gracefully. Inspect Activity before trying again."
			return
		}
	}
	for i, task := range plan.Tasks {
		if ctx.Err() != nil {
			c.setTask(i, "skipped", "Launch cancelled before dispatch.", "")
			continue
		}
		if task.Blocked != "" {
			c.setTask(i, "skipped", task.Blocked, "")
			continue
		}
		c.setTask(i, "starting", "", "")
		if task.DelaySeconds > 0 {
			timer := time.NewTimer(time.Duration(task.DelaySeconds) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				c.setTask(i, "skipped", "Cancelled during delay.", "")
				continue
			case <-timer.C:
			}
		}
		var err error
		var id domain.ID
		switch task.Kind {
		case "command":
			id, err = c.startRun(ctx, task, plan, launchID)
		case "application":
			var identity string
			identity, err = c.hooks.ReviewApplication(ctx, *task.Application)
			if err == nil && identity != task.Identity {
				err = errors.New("application changed after review")
			}
			if err == nil {
				err = c.hooks.LaunchApplication(ctx, *task.Application)
			}
		case "editor":
			var identity string
			identity, err = c.hooks.ReviewAction(ctx, *task.Action)
			if err == nil && identity != task.Identity {
				err = errors.New("editor changed after review")
			}
			if err == nil {
				err = c.hooks.LaunchAction(ctx, *task.Action)
			}
		case "preview":
			err = c.waitProject(ctx, task.ProjectID, plan)
			if err == nil {
				err = c.hooks.OpenURL(ctx, task.URL)
			}
		case "url":
			err = c.hooks.OpenURL(ctx, task.URL)
		}
		if err != nil {
			c.setTask(i, "failed", safeTaskError(task.Kind), id)
		} else if task.Kind == "command" {
			c.setTask(i, "running", "Worker dispatched; Activity shows readiness and output.", id)
		} else {
			c.setTask(i, "ready", "Desktop handoff accepted; window state is not verified.", id)
		}
	}
}

func (c *Controller) waitStopped(ctx context.Context, ids []domain.ID) error {
	ctx, cancel := context.WithTimeout(ctx, 125*time.Second)
	defer cancel()
	for len(ids) > 0 {
		remaining := make([]domain.ID, 0, len(ids))
		for _, id := range ids {
			snapshot, err := c.runs.Get(ctx, id)
			if err != nil {
				return err
			}
			if !terminal(snapshot.Value.State) {
				remaining = append(remaining, id)
			}
		}
		ids = remaining
		if len(ids) == 0 {
			return nil
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}
func safeTaskError(kind string) string {
	if kind == "command" {
		return "Command was not dispatched or an earlier attempt may still exist. Inspect Activity before retrying."
	}
	if kind == "preview" {
		return "Preview was not opened: the project did not become ready. Inspect its worker output."
	}
	return "Desktop handoff failed or configuration changed. Check installation and review again."
}
func (c *Controller) waitProject(ctx context.Context, id domain.ID, plan Plan) error {
	desired := map[string]bool{}
	for _, task := range plan.Tasks {
		if task.Kind == "command" && task.ProjectID == id {
			if task.Blocked != "" {
				return errors.New("a project command is blocked")
			}
			desired[executionIdentity(task.Command.Configuration)] = true
		}
	}
	if len(desired) == 0 {
		return errors.New("no managed command is configured for this preview")
	}
	ctx, cancel := context.WithTimeout(ctx, 310*time.Second)
	defer cancel()
	for {
		runs, err := c.runs.ListBounded(ctx, 256)
		if err != nil {
			return err
		}
		found := map[string]bool{}
		for _, snapshot := range runs {
			r := snapshot.Value
			key := executionIdentity(r.Command.Configuration)
			if !desired[key] || terminal(r.State) {
				continue
			}
			if r.State == "ready" || r.State == "running" && (r.Command.Configuration.Runtime == nil || r.Command.Configuration.Runtime.Readiness == nil) {
				found[key] = true
				continue
			}
			if r.State == "failed" || r.State == "unknown" {
				return errors.New("project unavailable")
			}
		}
		if len(found) == len(desired) {
			return nil
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (c *Controller) stopRun(ctx context.Context, req Request) Response {
	snapshot, err := c.runs.Get(ctx, req.ID)
	if err != nil {
		return failure("Run is unavailable.")
	}
	if terminal(snapshot.Value.State) {
		return Response{Message: "Command has already stopped."}
	}
	if err = signalWorker(snapshot.Value, req.Force); err != nil {
		return failure("Worker identity could not be verified. No signal was sent; inspect the process manually.")
	}
	return Response{Message: "Stop requested. Inspect Activity for completion. Unrelated applications are preserved."}
}
func (c *Controller) clearFinished(ctx context.Context) Response {
	runs, err := c.runs.ListBounded(ctx, 256)
	if err != nil {
		return failure("Runtime history cannot be read.")
	}
	for _, snapshot := range runs {
		r := snapshot.Value
		if !terminal(r.State) {
			continue
		}
		lease, e := runLock(c.paths, r)
		if e != nil {
			continue
		}
		e = c.runs.Delete(ctx, r.ID, snapshot.Revision)
		lease.Close()
		if e != nil {
			return failure("Some finished entries could not be removed. Reload Activity.")
		}
	}
	return Response{Message: "Finished runtime entries removed. Project source is preserved."}
}

func (c *Controller) reconcileRun(ctx context.Context, req Request) Response {
	snapshot, err := c.runs.Get(ctx, req.ID)
	if err != nil {
		return failure("Runtime entry is unavailable.")
	}
	r := snapshot.Value
	lease, err := runLock(c.paths, r)
	if err != nil {
		return failure("The worker still owns this command. Stop it explicitly before reconciling.")
	}
	defer lease.Close()
	if r.BootID == bootID() {
		if r.WorkerStart != "" && processStart(r.WorkerPID) == r.WorkerStart {
			return failure("The worker is still running. No ownership was changed.")
		}
		if r.PID > 0 && (processStart(r.PID) == r.ProcessStart || syscall.Kill(-r.PID, 0) == nil) {
			return failure("A process or group may still exist. Inspect it manually; no process was adopted or signalled.")
		}
	}
	if time.Since(r.StartedAt) < 10*time.Second {
		return failure("Allow the worker ten seconds to register before reconciling an interrupted dispatch.")
	}
	err = c.runs.Update(ctx, r.ID, snapshot.Revision, func(v domain.ManagedRun) (domain.ManagedRun, error) {
		v.State = "stopped"
		v.UpdatedAt = time.Now().UTC()
		v.Message = "Released worker and absent process group verified. A retry requires a fresh launch review."
		return v, nil
	})
	if err != nil {
		return failure("Reconciliation could not be saved. Reload Activity.")
	}
	return Response{Message: "Finished worker reconciled. Review the project again to retry."}
}

func (c *Controller) configureWorkspace(ctx context.Context, req Request) Response {
	if req.WorkspaceLaunch == nil || req.WorkspaceLaunch.Validate() != nil {
		return failure("Use valid HTTP(S) URLs, a literal editor command and a delay of 0–3600 seconds.")
	}
	err := storage.WithDirectoryLock(ctx, c.operations, func() error {
		return c.profiles.Update(ctx, req.ID, req.Revision, func(w domain.Workspace) (domain.Workspace, error) {
			w.Launch = req.WorkspaceLaunch
			w.UpdatedAt = time.Now().UTC()
			return w, nil
		})
	})
	if err != nil {
		return failure("Workspace changed or could not be saved. Reload before editing again.")
	}
	return Response{Message: "Workspace launch preferences saved. Review its launch before execution."}
}

func (c *Controller) saveSettings(ctx context.Context, req Request) Response {
	if req.Settings == nil || req.Settings.Validate() != nil {
		return failure("Choose valid startup settings and targets.")
	}
	if req.Settings.Autostart {
		return failure("Enabling login startup requires its exact launch review.")
	}
	err := storage.WithDirectoryLock(ctx, c.operations, func() error {
		return c.settings.Update(ctx, req.Revision, func(domain.Settings) (domain.Settings, error) { return *req.Settings, nil })
	})
	if err != nil {
		return failure("Settings changed or could not be saved. Reload before trying again.")
	}
	if err := writeAutostart(c.paths, c.executable, false); err != nil {
		return failure("Settings saved with login startup disabled, but its desktop entry could not be updated. Login mode checks saved settings before any launch.")
	}
	return Response{Message: "Settings saved. Login startup is disabled."}
}

// Keep bounded public errors; source paths/argv appear only in the explicit plan.
func cleanName(value string) string { return strings.TrimSpace(value) }
