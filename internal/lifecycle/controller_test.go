package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

var testContext = context.Background()

func testID(suffix byte) domain.ID { return domain.ID(strings.Repeat(string(suffix), 32)) }
func fixtureReview(ctx context.Context, a domain.CommandAction) (string, error) {
	info, err := os.Stat(a.Executable)
	if err != nil {
		return "", err
	}
	cwd, err := os.Stat(a.WorkingDirectory)
	if err != nil || !cwd.IsDir() {
		return "", errors.New("missing directory")
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%s", a.Executable, info.Size(), info.ModTime().UnixNano(), a.WorkingDirectory)))
	return hex.EncodeToString(h[:]), ctx.Err()
}
func fixtureController(t *testing.T) (*Controller, *atomic.Int32) {
	t.Helper()
	base := t.TempDir()
	paths := storage.Paths{ConfigDir: filepath.Join(base, "config", "workspace-manager"), StateDir: filepath.Join(base, "state", "workspace-manager"), CacheDir: filepath.Join(base, "cache", "workspace-manager")}
	var handoffs atomic.Int32
	hooks := Hooks{ReviewAction: fixtureReview, LaunchAction: func(context.Context, domain.CommandAction) error { handoffs.Add(1); return nil }, ReviewApplication: func(context.Context, domain.WorkspaceApplication) (string, error) { return "desktop-v1", nil }, LaunchApplication: func(context.Context, domain.WorkspaceApplication) error { handoffs.Add(1); return nil }, OpenURL: func(context.Context, string) error { handoffs.Add(1); return nil }, Shutdown: func(context.Context) error { handoffs.Add(1000); return nil }}
	c, err := NewController(paths, hooks, "/unused/test-owned-executable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, &handoffs
}
func addProject(t *testing.T, c *Controller, approved bool) domain.Project {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "owned-helper")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nprintf 'worker-output\\n'\ntrap 'exit 0' TERM INT\nwhile :; do sleep 0.1; done\n"), 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	p := domain.Project{ID: testID('a'), Name: "Owned fixture", Path: dir, Source: domain.SourceImported, CreatedAt: now, UpdatedAt: now, Commands: []domain.StartupCommand{{ID: testID('b'), Configuration: domain.CommandConfiguration{Name: "Fixture", Executable: exe, Arguments: []string{}, WorkingDirectory: dir}}}}
	if approved {
		p.Commands[0].Approval = &domain.CommandApproval{Fingerprint: p.Commands[0].Configuration.Fingerprint(), ApprovedAt: now}
	}
	if err := c.projects.Create(testContext, p); err != nil {
		t.Fatal(err)
	}
	return p
}
func addWorkspace(t *testing.T, c *Controller, p domain.Project) domain.Workspace {
	t.Helper()
	now := time.Now().UTC()
	w := domain.Workspace{ID: testID('c'), Name: "Coding", ProjectIDs: []domain.ID{p.ID}, Applications: []domain.WorkspaceApplication{{DesktopID: "org.example.Fixture.desktop", DuplicatePolicy: domain.SkipIfRunning}}, CreatedAt: now, UpdatedAt: now}
	if err := c.profiles.Create(testContext, w); err != nil {
		t.Fatal(err)
	}
	return w
}
func await(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("timed out waiting for fixture state")
}
func TestReviewNeverExecutesAndUnapprovedCommandsAreBlocked(t *testing.T) {
	c, calls := fixtureController(t)
	p := addProject(t, c, false)
	w := addWorkspace(t, c, p)
	review := c.Handle(testContext, Request{Action: "review_workspace", ID: w.ID})
	if review.Plan == nil || len(review.Plan.Tasks) != 2 || calls.Load() != 0 {
		t.Fatal(review, calls.Load())
	}
	if review.Plan.Tasks[1].Blocked == "" {
		t.Fatal("unapproved command was not blocked")
	}
	if got := c.Handle(testContext, Request{Action: "commit", Token: review.Plan.Token, SwitchPolicy: "cancel"}); got.Code != "cancelled" {
		t.Fatal(got)
	}
	if calls.Load() != 0 {
		t.Fatal("cancel dispatched a task")
	}
}
func TestChangedExecutableAndReviewExpiryRefuseCommit(t *testing.T) {
	c, calls := fixtureController(t)
	p := addProject(t, c, true)
	review := c.Handle(testContext, Request{Action: "review_project", ID: p.ID})
	if review.Plan == nil {
		t.Fatal(review)
	}
	if err := os.WriteFile(p.Commands[0].Configuration.Executable, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := c.Handle(testContext, Request{Action: "commit", Token: review.Plan.Token, SwitchPolicy: "keep"}); got.Code == "" {
		t.Fatal("changed executable accepted")
	}
	review = c.Handle(testContext, Request{Action: "review_project", ID: p.ID})
	c.plans[review.Plan.Token].plan.ExpiresAt = time.Now().Add(-time.Second)
	if got := c.Handle(testContext, Request{Action: "commit", Token: review.Plan.Token, SwitchPolicy: "keep"}); got.Code == "" {
		t.Fatal("expired review accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("unexpected handoff")
	}
}
func TestSessionBranchRevisionAndAncestry(t *testing.T) {
	c, _ := fixtureController(t)
	p := addProject(t, c, true)
	c.activeProjects = []domain.Project{p}
	c.activeApplications = []domain.WorkspaceApplication{{DesktopID: "org.example.Fixture.desktop", DuplicatePolicy: domain.SkipIfRunning}}
	saved := c.Handle(testContext, Request{Action: "capture", Name: "Monday"})
	if saved.Session == nil {
		t.Fatal(saved)
	}
	original := saved.Session
	branch := c.Handle(testContext, Request{Action: "branch", ID: original.Value.ID, Name: "Tuesday"})
	if branch.Session == nil || branch.Session.Value.ParentSessionID != original.Value.ID || len(branch.Session.Value.Applications) != 1 {
		t.Fatal(branch)
	}
	if got := c.Handle(testContext, Request{Action: "delete_session", ID: original.Value.ID, Revision: original.Revision}); got.Code == "" {
		t.Fatal("deleted parent")
	}
	rename := c.Handle(testContext, Request{Action: "rename_session", ID: branch.Session.Value.ID, Revision: branch.Session.Revision, Name: "Wednesday"})
	if rename.Code != "" {
		t.Fatal(rename)
	}
	current, _ := c.sessions.Get(testContext, branch.Session.Value.ID)
	history := c.Handle(testContext, Request{Action: "session_history", ID: current.Value.ID})
	if len(history.History) != 1 {
		t.Fatal(history)
	}
	restore := c.Handle(testContext, Request{Action: "restore_revision", ID: current.Value.ID, Revision: current.Revision, HistoryRevision: history.History[0]})
	if restore.Code != "" {
		t.Fatal(restore)
	}
	untouched, _ := c.sessions.Get(testContext, original.Value.ID)
	if untouched.Revision != original.Revision || untouched.Value.Name != "Monday" {
		t.Fatal("parent changed")
	}
}
func TestAllLoginModesDeduplicateAndInvalidateChangedConfiguration(t *testing.T) {
	for _, mode := range []domain.StartupMode{domain.StartupFixed, domain.StartupRestore, domain.StartupHybrid} {
		t.Run(string(mode), func(t *testing.T) {
			c, calls := fixtureController(t)
			p := addProject(t, c, true)
			w := addWorkspace(t, c, p)
			c.activeProjects = []domain.Project{p}
			c.activeApplications = domain.CloneApplications(w.Applications)
			snapshot := c.Handle(testContext, Request{Action: "capture", Name: "Snapshot"})
			if snapshot.Session == nil {
				t.Fatal(snapshot)
			}
			settings, err := c.getSettings(testContext)
			if err != nil {
				t.Fatal(err)
			}
			value := settings.Value
			value.Autostart = true
			value.StartupMode = mode
			value.DefaultWorkspaceID = w.ID
			value.RestoreSessionID = snapshot.Session.Value.ID
			review := c.Handle(testContext, Request{Action: "review_login", Settings: &value, Revision: settings.Revision})
			if review.Plan == nil || len(review.Plan.Tasks) != 2 {
				t.Fatal(review)
			}
			if calls.Load() != 0 {
				t.Fatal("login review launched")
			}
			enabled := c.Handle(testContext, Request{Action: "commit", Token: review.Plan.Token})
			if enabled.Code != "" {
				t.Fatal(enabled)
			}
			data, err := os.ReadFile(filepath.Join(filepath.Dir(c.paths.ConfigDir), "autostart", autostartFile))
			if err != nil || !strings.Contains(string(data), "Hidden=false") {
				t.Fatal(string(data), err)
			}
			// Only inspect the plan here: test fixtures must not invoke the real launcher.
			current, err := c.buildPlan(testContext, Request{Action: "review_login", Settings: &value})
			if err != nil || current.Fingerprint != review.Plan.Fingerprint {
				t.Fatal("login grant mismatch", err)
			}
			previous, _ := c.profiles.Get(testContext, w.ID)
			err = c.profiles.Update(testContext, w.ID, previous.Revision, func(v domain.Workspace) (domain.Workspace, error) {
				v.Launch = &domain.WorkspaceLaunch{URLs: []string{"https://example.test"}}
				v.UpdatedAt = time.Now().UTC()
				return v, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if mode != domain.StartupRestore {
				result := c.Handle(testContext, Request{Action: "login"})
				if result.Code == "" {
					t.Fatal("changed unattended launch accepted")
				}
			}
		})
	}
}
func TestShutdownSnapshotFailureCancellationAndMockHandoff(t *testing.T) {
	for _, mode := range []string{"save", "without_save", "save_failure", "cancel", "denied"} {
		t.Run(mode, func(t *testing.T) {
			c, calls := fixtureController(t)
			review := c.Handle(testContext, Request{Action: "review_shutdown"})
			if review.Plan == nil || calls.Load() != 0 {
				t.Fatal(review)
			}
			if mode == "save_failure" {
				c.sessions.Close()
			}
			if mode == "denied" {
				c.hooks.Shutdown = func(context.Context) error { return errors.New("denied") }
			}
			ctx := testContext
			if mode == "cancel" {
				cancelled, cancel := context.WithCancel(testContext)
				cancel()
				ctx = cancelled
			}
			result := c.Handle(ctx, Request{Action: "commit", Token: review.Plan.Token, Save: mode != "without_save", Name: "Before shutdown"})
			if mode == "save_failure" || mode == "cancel" {
				if result.Code == "" || calls.Load() != 0 {
					t.Fatal("failed capture/cancel shut down", result)
				}
				return
			}
			if result.Code != "" {
				t.Fatal(result)
			}
			await(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return !c.runtime.Busy })
			c.mu.Lock()
			state := c.runtime.ShutdownState
			c.mu.Unlock()
			if mode == "denied" {
				if state != "failed" {
					t.Fatal(state)
				}
			} else if state != "completed_request" || calls.Load() != 1000 {
				t.Fatal(state, calls.Load())
			}
			snapshots, err := c.sessions.List(testContext)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "without_save" && len(snapshots) != 1 {
				t.Fatal("snapshot missing")
			}
			_ = c.Handle(testContext, Request{Action: "commit", Token: review.Plan.Token, Save: true})
			if calls.Load() > 1000 {
				t.Fatal("shutdown replayed")
			}
		})
	}
}
func TestEnvironmentFilesAndOutputRedaction(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET='test-secret-value'\nPORT=3030\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config := domain.CommandConfiguration{WorkingDirectory: dir, Runtime: &domain.CommandRuntime{EnvironmentFiles: []string{".env"}}}
	env, secrets, err := commandEnvironment(config)
	if err != nil || !strings.Contains(strings.Join(env, "\n"), "PORT=3030") {
		t.Fatal(env, err)
	}
	buffer := &outputBuffer{secrets: secrets}
	buffer.Write([]byte("test-secret-value token=abc123 https://user:pass@example.test\x1b[31m\n"))
	out, _ := buffer.snapshot()
	if strings.Contains(out, "test-secret-value") || strings.Contains(out, "abc123") || strings.Contains(out, "user:pass") || strings.ContainsRune(out, '\x1b') {
		t.Fatal(out)
	}
	link := filepath.Join(dir, "linked")
	if err := os.Symlink(filepath.Join(dir, ".env"), link); err != nil {
		t.Fatal(err)
	}
	config.Runtime.EnvironmentFiles = []string{"linked"}
	if _, _, err := commandEnvironment(config); err == nil {
		t.Fatal("environment symlink accepted")
	}
}
func TestReadinessHTTPStatusNoRedirectAndTCP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/health", 302)
			return
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	check := domain.ReadinessCheck{Kind: "http", URL: server.URL + "/health", ExpectedStatus: 204, TimeoutSeconds: 1, IntervalSeconds: 1}
	if err := CheckReadiness(testContext, check); err != nil {
		t.Fatal(err)
	}
	check.URL = server.URL + "/redirect"
	if err := CheckReadiness(testContext, check); err == nil {
		t.Fatal("followed redirect")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := CheckReadiness(testContext, domain.ReadinessCheck{Kind: "tcp", Host: "localhost", Port: port, TimeoutSeconds: 1, IntervalSeconds: 1}); err != nil {
		t.Fatal(err)
	}
}
func TestPrivateIPCReconnectAndSingleton(t *testing.T) {
	c, _ := fixtureController(t)
	ctx, cancel := context.WithCancel(testContext)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, c) }()
	await(t, func() bool {
		_, err := callController(testContext, c.paths, Request{Action: "state"})
		return err == nil
	})
	for range 2 {
		response, err := callController(testContext, c.paths, Request{Action: "state"})
		if err != nil || response.Runtime == nil {
			t.Fatal(response, err)
		}
	}
	if err := Serve(ctx, c); err != nil {
		t.Fatal("second controller should exit without replacing socket", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestWorkerHelper(t *testing.T) {
	if os.Getenv("WORKSPACE_MANAGER_WORKER_TEST") != "1" {
		return
	}
	paths, err := storage.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := RunWorker(testContext, paths, domain.ID(os.Args[len(os.Args)-1]), fixtureReview); err != nil {
		t.Fatal(err)
	}
}
func TestIndependentWorkerOutputStopAndControllerReconnect(t *testing.T) {
	c, _ := fixtureController(t)
	p := addProject(t, c, true)
	t.Setenv("WORKSPACE_MANAGER_WORKER_TEST", "1")
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(c.paths.ConfigDir))
	t.Setenv("XDG_STATE_HOME", filepath.Dir(c.paths.StateDir))
	t.Setenv("XDG_CACHE_HOME", filepath.Dir(c.paths.CacheDir))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(t.TempDir(), "worker-wrapper")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run=^TestWorkerHelper$ -- \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	c.executable = wrapper
	review := c.Handle(testContext, Request{Action: "review_project", ID: p.ID})
	if review.Plan == nil {
		t.Fatal(review)
	}
	request := Request{Action: "commit", Token: review.Plan.Token, SwitchPolicy: "keep"}
	if result := c.Handle(testContext, request); result.Code != "" {
		t.Fatal(result)
	}
	_ = c.Handle(testContext, request)
	var run domain.ManagedRun
	await(t, func() bool {
		values, err := c.runs.List(testContext)
		if err != nil || len(values) != 1 {
			return false
		}
		run = values[0].Value
		return run.State == "running" && strings.Contains(run.Output, "worker-output")
	})
	t.Cleanup(func() {
		if processStart(run.WorkerPID) == run.WorkerStart {
			_ = signalWorker(run, true)
		}
	})
	// Reopening a controller does not own, kill or duplicate the independent worker.
	second, err := NewController(c.paths, c.hooks, wrapper)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	state := second.Handle(testContext, Request{Action: "state"})
	if state.Runtime == nil || len(state.Runtime.Runs) != 1 || state.Runtime.Runs[0].State != "running" {
		t.Fatal(state)
	}
	duplicate := second.Handle(testContext, Request{Action: "review_project", ID: p.ID})
	_ = second.Handle(testContext, Request{Action: "commit", Token: duplicate.Plan.Token, SwitchPolicy: "keep"})
	await(t, func() bool { second.mu.Lock(); defer second.mu.Unlock(); return !second.runtime.Busy })
	values, _ := second.runs.List(testContext)
	if len(values) != 1 {
		t.Fatal("duplicate worker created")
	}
	oldProject, _ := c.projects.Get(testContext, p.ID)
	if err := c.projects.Delete(testContext, p.ID, oldProject.Revision); err != nil {
		t.Fatal(err)
	}
	alias := p
	alias.ID = testID('d')
	alias.Commands = domain.CloneCommands(p.Commands)
	alias.Commands[0].ID = testID('e')
	alias.Preferences = &domain.ProjectPreferences{PreviewURL: "http://127.0.0.1:3000"}
	alias.Preferences.Approval = &domain.CommandApproval{Fingerprint: alias.Preferences.Fingerprint(), ApprovedAt: time.Now().UTC()}
	if err := c.projects.Create(testContext, alias); err != nil {
		t.Fatal(err)
	}
	reimport := second.Handle(testContext, Request{Action: "review_project", ID: alias.ID})
	if reimport.Plan == nil {
		t.Fatal(reimport)
	}
	if result := second.Handle(testContext, Request{Action: "commit", Token: reimport.Plan.Token, SwitchPolicy: "keep"}); result.Code != "" {
		t.Fatal(result)
	}
	await(t, func() bool { second.mu.Lock(); defer second.mu.Unlock(); return !second.runtime.Busy })
	values, _ = second.runs.List(testContext)
	if len(values) != 1 || values[0].Value.ID != run.ID {
		t.Fatal("reimported IDs duplicated the same server")
	}
	second.mu.Lock()
	previewState := second.runtime.Tasks[len(second.runtime.Tasks)-1].State
	second.mu.Unlock()
	if previewState != "ready" {
		t.Fatal("reused server preview failed", previewState)
	}
	if err := signalWorker(run, false); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool {
		current, err := second.runs.Get(testContext, run.ID)
		return err == nil && current.Value.State == "stopped"
	})
	history, err := second.runs.History(testContext, run.ID)
	if err != nil || len(history) != 1 {
		t.Fatal("log ticks must not create unbounded backups", len(history), err)
	}
	// Signalling a record with a mismatched start identity must never touch a PID.
	run.WorkerPID = os.Getpid()
	run.WorkerStart = "wrong"
	if signalWorker(run, true) == nil {
		t.Fatal("recycled identity signalled")
	}
}
func TestDesktopExecEscapingAndAutostartOwnership(t *testing.T) {
	c, _ := fixtureController(t)
	quoted, err := DesktopExec("/tmp/a b/%c\"$`\\tool")
	if err != nil || !strings.Contains(quoted, "%%c") || !strings.HasPrefix(quoted, "\"") {
		t.Fatal(quoted, err)
	}
	if _, err := DesktopExec("/tmp/a\nBad=true"); err == nil {
		t.Fatal("newline accepted")
	}
	if got, _ := DesktopExec("/tmp/a\\b"); got != `"/tmp/a\\\\b"` {
		t.Fatal("desktop backslash escaping", got)
	}
	root, err := openAutostart(c.paths)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	file, err := root.OpenFile(autostartFile, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("[Desktop Entry]\nName=User-owned\n")
	file.Close()
	if err := writeAutostart(c.paths, "/tmp/fixture", true); err == nil {
		t.Fatal("overwrote unowned entry")
	}
}

func TestLoginDispatchOnceAcrossControllerRestart(t *testing.T) {
	c, calls := fixtureController(t)
	p := addProject(t, c, true)
	snapshot, _ := c.projects.Get(testContext, p.ID)
	if err := c.projects.Update(testContext, p.ID, snapshot.Revision, func(v domain.Project) (domain.Project, error) { v.Commands = nil; return v, nil }); err != nil {
		t.Fatal(err)
	}
	w := addWorkspace(t, c, p)
	settings, err := c.getSettings(testContext)
	if err != nil {
		t.Fatal(err)
	}
	value := settings.Value
	value.Autostart = true
	value.DefaultWorkspaceID = w.ID
	review := c.Handle(testContext, Request{Action: "review_login", Settings: &value, Revision: settings.Revision})
	if review.Plan == nil {
		t.Fatal(review)
	}
	if r := c.Handle(testContext, Request{Action: "commit", Token: review.Plan.Token}); r.Code != "" {
		t.Fatal(r)
	}
	if r := c.Handle(testContext, Request{Action: "login"}); r.Code != "" {
		t.Fatal(r)
	}
	await(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return !c.runtime.Busy })
	if calls.Load() != 1 {
		t.Fatal("login was not dispatched", calls.Load())
	}
	if r := c.Handle(testContext, Request{Action: "login"}); r.Code != "" {
		t.Fatal(r)
	}
	if calls.Load() != 1 {
		t.Fatal("login replayed")
	}
	second, err := NewController(c.paths, c.hooks, c.executable)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.runtime.ActiveWorkspaceID != w.ID || len(second.activeProjects) != 1 {
		t.Fatal("launch metadata not recovered")
	}
	if r := second.Handle(testContext, Request{Action: "login"}); r.Code != "" {
		t.Fatal(r)
	}
	if calls.Load() != 1 {
		t.Fatal("restart replayed login")
	}
}

func TestCancellationDuringDelayPreservesExternalApplications(t *testing.T) {
	c, calls := fixtureController(t)
	p := addProject(t, c, false)
	w := addWorkspace(t, c, p)
	old, _ := c.profiles.Get(testContext, w.ID)
	if err := c.profiles.Update(testContext, w.ID, old.Revision, func(v domain.Workspace) (domain.Workspace, error) {
		v.Launch = &domain.WorkspaceLaunch{DelaySeconds: 30}
		return v, nil
	}); err != nil {
		t.Fatal(err)
	}
	review := c.Handle(testContext, Request{Action: "review_workspace", ID: w.ID})
	if review.Plan == nil {
		t.Fatal(review)
	}
	if r := c.Handle(testContext, Request{Action: "commit", Token: review.Plan.Token, SwitchPolicy: "keep"}); r.Code != "" {
		t.Fatal(r)
	}
	_ = c.Handle(testContext, Request{Action: "cancel_launch"})
	await(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return !c.runtime.Busy })
	if calls.Load() != 0 {
		t.Fatal("cancelled task launched")
	}
}

func TestHybridDeduplicatesReimportedDirectory(t *testing.T) {
	c, _ := fixtureController(t)
	p := addProject(t, c, true)
	c.activeProjects = []domain.Project{p}
	saved := c.Handle(testContext, Request{Action: "capture", Name: "Old registration"})
	if saved.Session == nil {
		t.Fatal(saved)
	}
	old, _ := c.projects.Get(testContext, p.ID)
	if err := c.projects.Delete(testContext, p.ID, old.Revision); err != nil {
		t.Fatal(err)
	}
	alias := p
	alias.ID = testID('d')
	alias.Commands = domain.CloneCommands(p.Commands)
	alias.Commands[0].ID = testID('e')
	if err := c.projects.Create(testContext, alias); err != nil {
		t.Fatal(err)
	}
	w := addWorkspace(t, c, alias)
	settings := domain.DefaultSettings()
	settings.StartupMode = domain.StartupHybrid
	settings.DefaultWorkspaceID = w.ID
	settings.RestoreSessionID = saved.Session.Value.ID
	plan, err := c.buildPlan(testContext, Request{Action: "review_login", Settings: &settings})
	if err != nil || len(plan.Projects) != 1 || len(plan.Tasks) != 2 || len(plan.Warnings) == 0 {
		t.Fatal(plan, err)
	}
}
