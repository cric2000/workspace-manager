package projects

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/projects/bootstrap"
	"workspace-manager/internal/storage"
)

type SetupReview struct {
	Token      string               `json:"token"`
	Kind       string               `json:"kind"`
	Label      string               `json:"label"`
	Action     domain.CommandAction `json:"action"`
	Note       string               `json:"note"`
	Network    bool                 `json:"network"`
	ExpiresAt  time.Time            `json:"expiresAt"`
	ToolChecks []ToolVersionCommand `json:"toolChecks,omitempty"`
}
type SetupReviewResult struct {
	Review  *SetupReview `json:"review"`
	Code    string       `json:"code"`
	Message string       `json:"message"`
}
type SetupResult struct {
	Started    bool   `json:"started"`
	Running    bool   `json:"running"`
	Finished   bool   `json:"finished"`
	Succeeded  bool   `json:"succeeded"`
	ExitCode   int    `json:"exitCode"`
	Output     string `json:"output"`
	Truncated  bool   `json:"truncated"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	LogID      string `json:"logId,omitempty"`
	LogSaved   bool   `json:"logSaved,omitempty"`
	LogMessage string `json:"logMessage,omitempty"`
}
type pendingSetup struct {
	review   SetupReview
	creation *pendingCreation
	node     string
	tools    []fileIdentity
	inputs   []fileIdentity
	result   *SetupResult
	cancel   context.CancelFunc
	probes   []versionProbe
}

// ReviewSetup accepts only a held successful creation and a fixed setup kind.
// It never receives executable/argv input from the caller and never executes tools.
func (service *Service) ReviewSetup(ctx context.Context, creationToken, kind string) SetupReviewResult {
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.openRegistry(); err != nil {
		return setupReviewFailure(err)
	}
	creation := service.creations[creationToken]
	if creation == nil || creation.result == nil || !creation.result.Created || !creation.result.Registered || creation.publishedIdentity == nil {
		return setupReviewFailure(errReview)
	}
	if err := ctx.Err(); err != nil {
		return setupReviewFailure(err)
	}
	if creation.setupRunning {
		return SetupReviewResult{Code: "busy", Message: "Setup is already running for this project. Check its outcome before continuing."}
	}
	if previous := creation.setupAttempts[kind]; previous != nil && previous.result != nil && (previous.result.Started || previous.result.Running) {
		message := "Setup was already attempted. Its files were kept; inspect the project before performing further setup manually."
		if previous.result.Succeeded {
			message = "This setup step already completed. It will not run again."
		}
		return SetupReviewResult{Code: "setup_attempted", Message: message}
	}
	current, err := service.registry.Get(ctx, creation.project.ID)
	if err != nil || current.Value.Path != creation.project.Path {
		return setupReviewFailure(errReview)
	}
	action, note, label, node, network, err := setupCommand(creation, kind)
	if err != nil {
		return setupReviewFailure(err)
	}
	directory, err := openSetupDirectory(creation.project.Path)
	if err != nil {
		return setupReviewFailure(err)
	}
	defer directory.Close()
	inputs, err := setupSnapshot(creation, kind, directory)
	if err != nil {
		return setupReviewFailure(err)
	}
	token, err := domain.NewID()
	if err != nil {
		return setupReviewFailure(err)
	}
	review := SetupReview{Token: string(token), Kind: kind, Label: label, Action: action, Note: note, Network: network, ExpiresAt: service.now().UTC().Add(5 * time.Minute)}
	tools, err := setupTools(review, node)
	if err != nil {
		return setupReviewFailure(err)
	}
	for key, pending := range service.setups {
		if (pending.result == nil || !pending.result.Running) && !service.now().Before(pending.review.ExpiresAt) {
			delete(service.setups, key)
		}
	}
	if len(service.setups) >= 64 {
		return SetupReviewResult{Code: "busy", Message: "Too many setup reviews are open. Wait for older reviews to expire."}
	}
	probes, err := setupVersionProbes(review, creation.review.Plan.TemplateID, node)
	if err != nil {
		return setupReviewFailure(err)
	}
	review.ToolChecks = versionCommands(probes)
	service.setups[review.Token] = &pendingSetup{review: review, creation: creation, node: node, tools: tools, inputs: inputs, probes: probes}
	creation.review.ExpiresAt = review.ExpiresAt
	copy := cloneSetupReview(review)
	return SetupReviewResult{Review: &copy}
}

func setupCommand(creation *pendingCreation, kind string) (domain.CommandAction, string, string, string, bool, error) {
	path := creation.project.Path
	template := creation.review.Plan.TemplateID
	action := domain.CommandAction{WorkingDirectory: path, Arguments: []string{}}
	note, label, node := "", "", ""
	network := false
	tool := func(program string) (string, error) {
		value, err := exec.LookPath(program)
		if err != nil {
			return "", errExecutable
		}
		value, err = filepath.EvalSymlinks(value)
		if err != nil {
			return "", errExecutable
		}
		if _, err := identity(value, true); err != nil {
			return "", errExecutable
		}
		return value, nil
	}
	var err error
	switch kind {
	case "git":
		action.Executable, err = tool("git")
		action.Arguments = []string{"-c", "core.hooksPath=/dev/null", "init", "--initial-branch=main", "--template=", "--", "."}
		label = "Initialize Git"
		note = "Creates an empty .git repository on branch main. No files are staged, no commit is made, and no remote is configured. Git templates and user/system configuration are disabled. An existing .git entry is never reused."
	case "environment":
		if template != "python" && template != "fastapi" {
			err = errDraft
			break
		}
		action.Executable, err = tool("python3")
		action.Arguments = []string{"-I", "-m", "venv", ".venv"}
		label = "Create virtual environment"
		note = "Creates a new .venv and bootstraps its bundled pip. An existing .venv is never reused. Python startup settings remain unchanged; review the saved startup command separately. Ubuntu may require python3-venv."
	case "dependencies":
		network = true
		label = "Install dependencies"
		if template == "fastapi" {
			action.Executable = filepath.Join(path, ".venv/bin/python")
			action.Arguments = []string{"-I", "-B", "-m", "pip", "--isolated", "--require-virtualenv", "--disable-pip-version-check", "--no-cache-dir", "--no-color", "install", "--no-input", "--index-url", "https://pypi.org/simple", ".[test]"}
			note = "Downloads FastAPI/test dependencies and build tools into this flow's virtual environment. pip may execute package build code, including the declared setuptools build backend. Only the unchanged generated starter is accepted. No startup server runs."
		} else if template == "node-typescript" || template == "nextjs" {
			program := creation.review.Plan.DependencySetup.Program
			action.Executable, err = tool(program)
			if err != nil {
				break
			}
			node, err = tool("node")
			if err != nil {
				break
			}
			if program == "npm" {
				action.Arguments = []string{"--prefix", ".", "install", "--ignore-scripts", "--workspaces=false", "--no-audit", "--no-fund"}
			} else if program == "pnpm" {
				action.Arguments = []string{"--dir", ".", "--ignore-workspace", "install", "--ignore-scripts", "--ignore-pnpmfile", "--registry", "https://registry.npmjs.org/"}
			} else {
				err = errDraft
				break
			}
			if nodeScript(action.Executable) {
				action.Arguments = append([]string{action.Executable}, action.Arguments...)
				action.Executable = node
			}
			note = "Downloads dependencies and creates a lockfile in this project. Lifecycle scripts and workspace-wide installs are disabled; pnpm hooks are disabled. Only the unchanged generated starter is accepted. Some native packages may need separate build approval later. No startup command runs."
		} else {
			err = errDraft
		}
	default:
		err = errDraft
	}
	if err == nil && action.Validate() != nil {
		err = errDraft
	}
	return action, note, label, node, network, err
}
func setupTools(review SetupReview, node string) ([]fileIdentity, error) {
	ids, err := executableIdentities(review.Action.Executable)
	if err != nil {
		return nil, err
	}
	for _, path := range []string{node} {
		if path != "" {
			id, err := identity(path, true)
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
	}
	if len(review.Action.Arguments) > 0 && filepath.IsAbs(review.Action.Arguments[0]) {
		id, err := identity(review.Action.Arguments[0], true)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// CommitSetup is at most once per held token. Cancellation may leave partial
// tool output in the project; files are preserved and never silently retried.
func (service *Service) CommitSetup(ctx context.Context, token string) SetupResult {
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return setupFailure(storage.ErrClosed)
	}
	pending := service.setups[token]
	if pending == nil {
		service.mu.Unlock()
		return setupFailure(errReview)
	}
	if pending.result != nil {
		result := *pending.result
		service.mu.Unlock()
		return result
	}
	fail := func(err error) SetupResult {
		result := setupFailure(err)
		pending.result = &result
		service.mu.Unlock()
		return result
	}
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	if !service.now().Before(pending.review.ExpiresAt) {
		return fail(errReview)
	}
	creation := pending.creation
	if creation.setupRunning {
		return setupResultUnlock(service, SetupResult{Code: "busy", Message: "Another setup step is running for this project."})
	}
	if previous := creation.setupAttempts[pending.review.Kind]; previous != nil && previous.result != nil && (previous.result.Started || previous.result.Running) {
		return fail(errReview)
	}
	current, err := service.registry.Get(ctx, creation.project.ID)
	if err != nil || current.Value.Path != creation.project.Path {
		return fail(errReview)
	}
	tools, err := setupTools(pending.review, pending.node)
	if err != nil || !slices.Equal(tools, pending.tools) {
		return fail(errReview)
	}
	directory, err := openSetupDirectory(creation.project.Path)
	if err != nil {
		return fail(err)
	}
	inputs, err := setupSnapshot(creation, pending.review.Kind, directory)
	if err != nil || !slices.Equal(inputs, pending.inputs) {
		directory.Close()
		if err == nil {
			err = errReview
		}
		return fail(err)
	}
	select {
	case service.preparationSlot <- struct{}{}:
	default:
		directory.Close()
		return setupResultUnlock(service, SetupResult{Code: "busy", Message: "Another setup or preparation is running. Wait, then confirm this review again."})
	}
	logCtx, logCancel := context.WithTimeout(ctx, 5*time.Second)
	log, err := service.logStart(logCtx, service.resolvedPaths, creation.project.ID, pending.review, service.now().UTC())
	logCancel()
	if err != nil {
		<-service.preparationSlot
		directory.Close()
		result := SetupResult{Finished: true, ExitCode: -1, Code: "setup_history", Message: "Setup did not start because its attempt could not be saved. Check the app state directory's permissions and stored records. Request a new setup review after fixing storage."}
		pending.result = &result
		return setupResultUnlock(service, result)
	}
	defer log.Close()
	checkpoint := domain.BootstrapRecovery{ID: log.LogID(), ProjectID: creation.project.ID, Path: creation.project.Path, Kind: pending.review.Kind, RecordedAt: service.now().UTC()}
	checkpointCtx, checkpointCancel := context.WithTimeout(ctx, 5*time.Second)
	lease, checkpointErr := startRecoveryCheckpoint(checkpointCtx, service.resolvedPaths, checkpoint)
	checkpointCancel()
	if checkpointErr != nil {
		<-service.preparationSlot
		directory.Close()
		result := SetupResult{Finished: true, ExitCode: -1, Code: "setup_history", Message: "Setup did not start because its recovery checkpoint could not be saved. Check app state storage and request a new review."}
		finishSetupLog(ctx, log, &result, service.now().UTC())
		pending.result = &result
		return setupResultUnlock(service, result)
	}
	defer lease.Close()
	// Saving may wait for filesystem locks. Recheck the held inputs immediately
	// before launching rather than extending the earlier inspection window.
	tools, err = setupTools(pending.review, pending.node)
	inputsNow, inputErr := setupSnapshot(creation, pending.review.Kind, directory)
	if err != nil || inputErr != nil || !slices.Equal(tools, pending.tools) || !slices.Equal(inputsNow, pending.inputs) || ctx.Err() != nil {
		failure := errReview
		if ctx.Err() != nil {
			failure = ctx.Err()
		}
		result := setupFailure(failure)
		finishSetupLog(ctx, log, &result, service.now().UTC())
		pending.result = &result
		<-service.preparationSlot
		directory.Close()
		return setupResultUnlock(service, result)
	}
	if creation.setupAttempts == nil {
		creation.setupAttempts = make(map[string]*pendingSetup)
	}
	creation.setupAttempts[pending.review.Kind] = pending
	creation.setupRunning = true
	runCtx, cancel := context.WithTimeout(ctx, 11*time.Minute)
	pending.cancel = cancel
	pending.result = &SetupResult{Running: true, ExitCode: -1, Code: "setup_running", Message: "The reviewed setup is running. Check again for its final outcome."}
	action := cloneSetupReview(pending.review).Action
	node := pending.node
	runner := service.setupRunner
	service.mu.Unlock()
	executed := runner(runCtx, action, node, directory)
	if executed.Err == nil {
		executed.Err = runCtx.Err()
	}
	cancel()
	service.mu.Lock()
	defer service.mu.Unlock()
	defer directory.Close()
	<-service.preparationSlot
	pending.cancel = nil
	creation.setupRunning = false
	result := SetupResult{Started: executed.Started, Finished: true, Succeeded: executed.Err == nil && executed.ExitCode == 0, ExitCode: executed.ExitCode, Output: executed.Output, Truncated: executed.Truncated}
	result.Output, result.Truncated = bootstrap.SafeSetupOutput(result.Output, result.Truncated)
	if !result.Succeeded {
		result.Code = "setup_failed"
		result.Message = "Setup failed. Inspect the output and project files before continuing."
		if errors.Is(executed.Err, context.Canceled) || errors.Is(executed.Err, context.DeadlineExceeded) {
			result.Code = "cancelled"
			result.Message = "Setup was cancelled or timed out."
		}
		if result.Started {
			result.Message += " Partial setup files were kept. This step will not retry automatically."
		} else {
			result.Message += " No setup process started."
		}
	} else if err := recordSetupOutput(creation, pending.review.Kind, directory); err != nil {
		result.Succeeded = false
		result.Code = "setup_uncertain"
		result.Message = "The tool exited successfully, but its project location or output could not be confirmed. Files were kept; inspect the project before continuing."
	} else {
		result.Message = pending.review.Label + " completed. Startup approval was not changed."
	}
	finishSetupLog(ctx, log, &result, service.now().UTC())
	pending.result = &result
	// Give the next setup step a full review window even after a slow install.
	creation.review.ExpiresAt = service.now().UTC().Add(5 * time.Minute)
	return result
}
func setupResultUnlock(service *Service, result SetupResult) SetupResult {
	service.mu.Unlock()
	return result
}

func recordSetupOutput(creation *pendingCreation, kind string, root *os.File) error {
	current, err := identity(creation.project.Path, false)
	if err != nil || creation.publishedIdentity == nil || current != *creation.publishedIdentity {
		return errReview
	}
	switch kind {
	case "git":
		id, err := identity(filepath.Join(creation.project.Path, ".git"), false)
		if err != nil {
			return err
		}
		creation.gitIdentity = &id
	case "environment":
		id, err := identity(filepath.Join(creation.project.Path, ".venv"), false)
		if err != nil {
			return err
		}
		cfg, err := setupRegularID(root, ".venv/pyvenv.cfg", filepath.Join(creation.project.Path, ".venv/pyvenv.cfg"))
		if err != nil {
			return err
		}
		creation.environmentIdentity, creation.environmentConfig = &id, &cfg
	case "dependencies":
		if creation.review.Plan.TemplateID != "fastapi" {
			if _, err := identity(filepath.Join(creation.project.Path, "node_modules"), false); err != nil {
				return err
			}
		}
	}
	return nil
}

// GetSetupOutcome is read-only even for an unused token or a lost/cancelled call.
func (service *Service) GetSetupOutcome(ctx context.Context, token string) SetupResult {
	service.mu.Lock()
	defer service.mu.Unlock()
	if ctx.Err() != nil {
		return setupFailure(ctx.Err())
	}
	if service.closed {
		return setupFailure(storage.ErrClosed)
	}
	pending := service.setups[token]
	if pending == nil {
		return setupFailure(errReview)
	}
	if pending.result != nil {
		return *pending.result
	}
	return SetupResult{Code: "setup_pending", Message: "No setup outcome is available. Check again, or inspect the project before starting a new review."}
}
func cloneSetupReview(review SetupReview) SetupReview {
	review.ToolChecks = cloneVersionCommands(review.ToolChecks)
	review.Action.Arguments = slices.Clone(review.Action.Arguments)
	return review
}
func setupReviewFailure(err error) SetupReviewResult {
	result := setupFailure(err)
	return SetupReviewResult{Code: result.Code, Message: result.Message}
}
func setupFailure(err error) SetupResult {
	result := SetupResult{Finished: true, ExitCode: -1}
	switch {
	case errors.Is(err, errSetupExists):
		result.Code, result.Message = "setup_exists", "Setup output already exists, including a directory, file or symlink. It will not be reused or replaced; inspect it and perform any further setup manually."
	case errors.Is(err, errSetupSource):
		result.Code, result.Message = "setup_source", "Dependency setup requires unchanged generated starter files and this flow's virtual environment. Remove unreviewed configuration/hooks or perform setup manually after reviewing your changes."
	default:
		failure := registryFailure(err)
		result.Code, result.Message = failure.Code, failure.Message
	}
	return result
}
