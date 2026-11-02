package projects

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"golang.org/x/sys/unix"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/projects/bootstrap"
	"workspace-manager/internal/storage"
)

type CreationDraft struct {
	TemplateID      string                 `json:"templateId"`
	Name            string                 `json:"name"`
	ParentDirectory string                 `json:"parentDirectory"`
	ModulePath      string                 `json:"modulePath"`
	Next            *bootstrap.NextOptions `json:"next,omitempty"`
}

type CreationReview struct {
	Token           string                      `json:"token"`
	ExpiresAt       time.Time                   `json:"expiresAt"`
	Path            string                      `json:"path"`
	Plan            bootstrap.Plan              `json:"plan"`
	Startup         domain.CommandConfiguration `json:"startup"`
	ToolRequirement string                      `json:"toolRequirement"`
	DependencySetup *domain.CommandAction       `json:"dependencySetup,omitempty"`
	AdditionalSetup []domain.CommandAction      `json:"additionalSetup,omitempty"`
	Scaffold        *domain.CommandAction       `json:"scaffold,omitempty"`
	Prepared        bool                        `json:"prepared,omitempty"`
	ToolChecks      []ToolVersionCommand        `json:"toolChecks,omitempty"`
}

type CreationReviewResult struct {
	Review  *CreationReview        `json:"review"`
	Code    string                 `json:"code"`
	Message string                 `json:"message"`
	History *CreationHistoryStatus `json:"history,omitempty"`
}

type CreationResult struct {
	Created      bool                   `json:"created"`
	Registered   bool                   `json:"registered"`
	Path         string                 `json:"path"`
	RecoveryPath string                 `json:"recoveryPath"`
	State        *RegistryState         `json:"state"`
	Logs         []string               `json:"logs"`
	Code         string                 `json:"code"`
	Message      string                 `json:"message"`
	History      *CreationHistoryStatus `json:"history,omitempty"`
}

type pendingCreation struct {
	review              CreationReview
	parent              bootstrap.ParentIdentity
	executables         []fileIdentity
	project             domain.Project
	result              *CreationResult
	preparing           bool
	preparation         *CreationReviewResult
	cancel              context.CancelFunc
	publishedIdentity   *fileIdentity
	gitIdentity         *fileIdentity
	environmentIdentity *fileIdentity
	environmentConfig   *fileIdentity
	setupRunning        bool
	setupAttempts       map[string]*pendingSetup
	probes              []versionProbe
}

func (service *Service) ListTemplates() []bootstrap.Definition { return bootstrap.NewRegistry().List() }

// ReviewCreation renders a held, exact file plan. No project directory is
// created, and neither template executables nor generated source are executed.
func (service *Service) ReviewCreation(ctx context.Context, draft CreationDraft) CreationReviewResult {
	registry := bootstrap.NewRegistry()
	plan, err := registry.Render(draft.TemplateID, bootstrap.Options{Name: draft.Name, ModulePath: draft.ModulePath, Next: draft.Next})
	if err != nil {
		return creationReviewFailure(err)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.openRegistry(); err != nil {
		return creationReviewFailure(err)
	}
	parent, err := bootstrap.OpenParent(draft.ParentDirectory)
	if err != nil {
		return creationReviewFailure(err)
	}
	defer parent.Close()
	if err := parent.Absent(draft.Name); err != nil {
		return creationReviewFailure(err)
	}
	if ctx.Err() != nil {
		return creationReviewFailure(ctx.Err())
	}
	definition, err := registry.Definition(plan.TemplateID)
	if err != nil {
		return creationReviewFailure(err)
	}
	tools := map[string]string{}
	identities := []fileIdentity{}
	for _, program := range bootstrap.SelectedTools(definition, plan) {
		executable, err := exec.LookPath(program)
		if err != nil {
			return creationReviewFailure(errExecutable)
		}
		executable, err = filepath.EvalSymlinks(executable)
		if err != nil {
			return creationReviewFailure(errExecutable)
		}
		tool, err := identity(executable, true)
		if err != nil {
			return creationReviewFailure(errExecutable)
		}
		tools[program] = executable
		identities = append(identities, tool)
	}
	now := service.now().UTC()
	id, err := domain.NewID()
	if err != nil {
		return creationReviewFailure(err)
	}
	commandID, err := domain.NewID()
	if err != nil {
		return creationReviewFailure(err)
	}
	token, err := domain.NewID()
	if err != nil {
		return creationReviewFailure(err)
	}
	path := filepath.Join(parent.Path, draft.Name)
	startupExecutable := tools[plan.StartupProgram]
	if plan.StartupProjectExecutable != "" {
		startupExecutable, err = plannedExecutable(path, plan.StartupProjectExecutable)
		if err != nil {
			return creationReviewFailure(err)
		}
	}
	startup := domain.CommandConfiguration{Name: plan.StartupName, Executable: startupExecutable, Arguments: slices.Clone(plan.StartupArguments), WorkingDirectory: path, Runtime: domain.CloneRuntime(plan.Runtime)}
	project := domain.Project{ID: id, Name: draft.Name, Path: path, Source: domain.SourceBootstrapped, CreatedAt: now, UpdatedAt: now, Commands: []domain.StartupCommand{{ID: commandID, Configuration: startup}}}
	if plan.PreviewURL != "" {
		project.Preferences = &domain.ProjectPreferences{PreviewURL: plan.PreviewURL}
	}
	if project.Validate() != nil {
		return creationReviewFailure(bootstrap.ErrOptions)
	}
	if service.creations == nil {
		service.creations = make(map[string]*pendingCreation)
	}
	for key, pending := range service.creations {
		if !pending.preparing && !pending.setupRunning && !now.Before(pending.review.ExpiresAt) {
			delete(service.creations, key)
		}
	}
	if len(service.creations) >= 64 {
		return CreationReviewResult{Code: "busy", Message: "Too many creation reviews are open. Close older reviews or wait for them to expire."}
	}
	review := CreationReview{Token: string(token), ExpiresAt: now.Add(5 * time.Minute), Path: path, Plan: plan, Startup: startup, ToolRequirement: definition.Requirements + " Executable presence checked. Version checks run only when requested below."}
	if plan.DependencySetup != nil {
		setup, err := plannedSetup(path, *plan.DependencySetup, tools)
		if err != nil {
			return creationReviewFailure(err)
		}
		review.DependencySetup = &setup
		if review.DependencySetup.Validate() != nil {
			return creationReviewFailure(bootstrap.ErrOptions)
		}
	}
	for _, setup := range plan.AdditionalSetup {
		action, err := plannedSetup(path, setup, tools)
		if err != nil {
			return creationReviewFailure(err)
		}
		review.AdditionalSetup = append(review.AdditionalSetup, action)
	}
	if plan.Scaffold != nil {
		action, err := plannedSetup(path, *plan.Scaffold, tools)
		if err != nil {
			return creationReviewFailure(err)
		}
		// Invoke the reviewed npm CLI with the reviewed Node binary explicitly;
		// npm's env-node shebang must not silently select a changed PATH alias.
		action.Arguments = append([]string{action.Executable}, action.Arguments...)
		action.Executable = tools["node"]
		action.WorkingDirectory = filepath.Join(os.TempDir(), "workspace-manager-next-"+review.Token)
		temporary, err := bootstrap.OpenParent(filepath.Dir(action.WorkingDirectory))
		if err != nil {
			return creationReviewFailure(err)
		}
		temporary.Close()
		review.Scaffold = &action
	}
	probes, err := creationVersionProbes(definition, plan, tools)
	if err != nil {
		return creationReviewFailure(err)
	}
	review.ToolChecks = versionCommands(probes)
	service.creations[review.Token] = &pendingCreation{review: review, parent: parent.Identity, executables: identities, project: project, probes: probes}
	copy := cloneCreationReview(review)
	return CreationReviewResult{Review: &copy}
}

// CommitCreation accepts only a server-held review token. Publication is the
// irreversible boundary: after it, registration failures preserve source and
// return Created=true. Replaying the token retrieves the same outcome.
func (service *Service) CommitCreation(ctx context.Context, token string) CreationResult {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		return creationFailure(storage.ErrClosed)
	}
	pending := service.creations[token]
	if pending == nil {
		return creationFailure(errReview)
	}
	if pending.result != nil {
		return cloneCreationResult(*pending.result)
	}
	if pending.review.Scaffold != nil && !pending.review.Prepared {
		return CreationResult{Code: "preparation_required", Message: "Prepare the official scaffolder output and review its files before creating the project."}
	}
	log, err := service.beginCreationHistory(ctx, pending, "publication")
	if err != nil {
		result := CreationResult{Code: "creation_history", Message: "Creation did not start because its attempt could not be saved. Check app state storage, then request a new creation review."}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			result = creationFailure(err)
		}
		pending.result = &result
		return result
	}
	defer log.Close()
	result := CreationResult{Logs: []string{}}
	err = storage.WithDirectoryLock(ctx, service.operations, func() error {
		if !service.now().Before(pending.review.ExpiresAt) {
			return errReview
		}
		parent, err := bootstrap.OpenParent(filepath.Dir(pending.review.Path))
		if err != nil {
			return errReview
		}
		defer parent.Close()
		if parent.Identity != pending.parent {
			return errReview
		}
		for _, tool := range pending.executables {
			current, err := identity(tool.Path, true)
			if err != nil || current != tool {
				return errReview
			}
		}
		records, err := service.registry.List(ctx)
		if err != nil {
			return err
		}
		for _, record := range records {
			if record.Value.Path == pending.review.Path {
				return errDuplicate
			}
		}
		publication, err := parent.Publish(ctx, pending.project.Name, pending.review.Plan.Files)
		result.Created = publication.Published
		result.RecoveryPath = publication.RecoveryPath
		if !publication.Published {
			return err
		}
		result.Path = pending.review.Path
		result.Logs = append(result.Logs, fmt.Sprintf("Generated %d reviewed files in a private staging directory.", len(pending.review.Plan.Files)), "Published the new project without replacing any existing entry.")
		if err != nil || !publication.Durable {
			result.Code = "creation_uncertain"
			result.Message = "Project files were published, but their location or durability could not be confirmed. Keep the files and inspect the destination before registering or creating again."
			return nil
		}
		publishedID, identityErr := identity(pending.project.Path, false)
		if identityErr != nil {
			result.Code = "creation_uncertain"
			result.Message = "Project files were published, but their directory identity could not be confirmed. Files were kept; inspect the destination before continuing."
			return nil
		}
		pending.publishedIdentity = &publishedID
		// Once files are published, finish the short metadata operation even if the
		// caller cancelled. Never roll back a user's now-visible project directory.
		finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err = service.registry.Create(finish, pending.project)
		var mutation *storage.MutationError
		result.Registered = err == nil || errors.As(err, &mutation) && mutation.Committed
		if err != nil {
			result.Code = "registration_failed"
			result.Message = "Project files were created and kept, but registration could not be confirmed. Reload registered projects; inspect this directory to register it if needed."
			return nil
		}
		result.Logs = append(result.Logs, "Saved the bootstrapped registration. Startup command still requires approval.")
		result.State, err = service.registryState(finish)
		if err != nil {
			result.Code = "refresh_required"
			result.Message = "Project created and registered. Reload registered projects before continuing."
		}
		return nil
	})
	if err != nil {
		failure := creationFailure(err)
		failure.RecoveryPath = result.RecoveryPath
		result = failure
		if result.RecoveryPath != "" {
			result.Message += " Temporary files were kept at the recovery path; inspect them before trying again."
		}
	}
	result.History = finishCreationHistory(ctx, log, publicationLogResult(result, len(pending.review.Plan.Files)), service.now().UTC(), token)
	pending.result = &result
	return cloneCreationResult(result)
}

// GetCreationOutcome is strictly read-only. In particular, querying an unused
// token after transport cancellation must never start the approved operation.
func (service *Service) GetCreationOutcome(ctx context.Context, token string) CreationResult {
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return creationFailure(err)
	}
	if service.closed {
		return creationFailure(storage.ErrClosed)
	}
	pending := service.creations[token]
	if pending == nil {
		return creationFailure(errReview)
	}
	if pending.result != nil {
		return cloneCreationResult(*pending.result)
	}
	return CreationResult{Code: "creation_pending", Message: "No completed creation outcome is available yet. Check again, or close and inspect the destination and registered projects before creating again."}
}

// Project-local executables may not exist until the displayed manual setup is
// completed. Only registry-owned relative paths are accepted; creation never runs them.
func plannedExecutable(root, relative string) (string, error) {
	if !fs.ValidPath(relative) || relative == "." || len(relative) > 1024 {
		return "", bootstrap.ErrOptions
	}
	return filepath.Join(root, relative), nil
}
func plannedSetup(root string, setup bootstrap.Setup, tools map[string]string) (domain.CommandAction, error) {
	executable := tools[setup.Program]
	if setup.ProjectExecutable != "" {
		var err error
		executable, err = plannedExecutable(root, setup.ProjectExecutable)
		if err != nil {
			return domain.CommandAction{}, err
		}
	}
	action := domain.CommandAction{Executable: executable, Arguments: slices.Clone(setup.Arguments), WorkingDirectory: root}
	if action.Validate() != nil {
		return action, bootstrap.ErrOptions
	}
	return action, nil
}

func cloneCreationReview(review CreationReview) CreationReview {
	review.ToolChecks = cloneVersionCommands(review.ToolChecks)
	if review.Scaffold != nil {
		action := *review.Scaffold
		action.Arguments = slices.Clone(action.Arguments)
		review.Scaffold = &action
	}
	if review.Plan.Scaffold != nil {
		setup := *review.Plan.Scaffold
		setup.Arguments = slices.Clone(setup.Arguments)
		review.Plan.Scaffold = &setup
	}
	review.Plan.Runtime = domain.CloneRuntime(review.Plan.Runtime)
	review.Startup.Runtime = domain.CloneRuntime(review.Startup.Runtime)
	review.Plan.AdditionalSetup = slices.Clone(review.Plan.AdditionalSetup)
	for i := range review.Plan.AdditionalSetup {
		review.Plan.AdditionalSetup[i].Arguments = slices.Clone(review.Plan.AdditionalSetup[i].Arguments)
	}
	review.AdditionalSetup = slices.Clone(review.AdditionalSetup)
	for i := range review.AdditionalSetup {
		review.AdditionalSetup[i].Arguments = slices.Clone(review.AdditionalSetup[i].Arguments)
	}
	review.Plan.Files = slices.Clone(review.Plan.Files)
	review.Plan.InitializationCommands = slices.Clone(review.Plan.InitializationCommands)
	review.Plan.StartupArguments = slices.Clone(review.Plan.StartupArguments)
	review.Startup.Arguments = slices.Clone(review.Startup.Arguments)
	if review.Plan.DependencySetup != nil {
		setup := *review.Plan.DependencySetup
		setup.Arguments = slices.Clone(setup.Arguments)
		review.Plan.DependencySetup = &setup
	}
	if review.DependencySetup != nil {
		setup := *review.DependencySetup
		setup.Arguments = slices.Clone(setup.Arguments)
		review.DependencySetup = &setup
	}
	return review
}

func cloneCreationResult(result CreationResult) CreationResult {
	if result.History != nil {
		history := *result.History
		result.History = &history
	}
	result.Logs = slices.Clone(result.Logs)
	result.State = cloneResult(RegistryResult{State: result.State}).State
	return result
}

func creationReviewFailure(err error) CreationReviewResult {
	result := creationFailure(err)
	return CreationReviewResult{Code: result.Code, Message: result.Message}
}
func creationFailure(err error) CreationResult {
	switch {
	case errors.Is(err, bootstrap.ErrModulePath):
		return CreationResult{Code: "validation", Message: "Use a valid Go module path such as example.com/you/project."}
	case errors.Is(err, bootstrap.ErrNodeName):
		return CreationResult{Code: "validation", Message: "Use 1–64 lowercase letters, digits or hyphens for the Node.js package/directory name, starting with a letter or digit."}
	case errors.Is(err, bootstrap.ErrPythonName):
		return CreationResult{Code: "validation", Message: "Use 1–64 lowercase letters, digits or hyphens for the Python project/directory name, starting and ending with a letter or digit."}
	case errors.Is(err, bootstrap.ErrOptions):
		return CreationResult{Code: "validation", Message: "Choose a supported template and use a project name of 1–64 ASCII letters, digits, hyphens or underscores, starting with a letter or digit. Follow the selected template's naming rules."}
	case errors.Is(err, bootstrap.ErrScaffold):
		return CreationResult{Code: "scaffolding_failed", Message: "The official scaffolder failed or produced unsupported output. No destination was created. Check network access, the selected tools and the project name, then start a new review."}
	case errors.Is(err, errExecutable):
		return CreationResult{Code: "executable", Message: "An executable required by this template is unavailable. Install its listed tools on PATH and review again."}
	case errors.Is(err, bootstrap.ErrExists):
		return CreationResult{Code: "destination_exists", Message: "The destination already exists. Choose a new project name; existing entries, including empty directories, are never replaced."}
	case errors.Is(err, bootstrap.ErrParent), errors.Is(err, bootstrap.ErrChanged):
		return CreationResult{Code: "parent_directory", Message: "Choose an existing absolute parent directory without symlink components. Check its access permissions and review again."}
	case errors.Is(err, fs.ErrPermission), errors.Is(err, unix.ENOSPC), errors.Is(err, unix.EDQUOT), errors.Is(err, unix.EROFS):
		return CreationResult{Code: "creation_storage", Message: "Could not write the new project. Check parent directory permissions, available space and filesystem access, then review again."}
	default:
		failure := registryFailure(err)
		return CreationResult{Code: failure.Code, Message: failure.Message}
	}
}
