package projects

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

type ProjectView struct {
	Project  domain.Project   `json:"project"`
	Revision storage.Revision `json:"revision"`
}

type RegistryState struct {
	Projects []ProjectView `json:"projects"`
}

type RegistryResult struct {
	State     *RegistryState `json:"state"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Committed bool           `json:"committed"`
}

// Draft has no approval or server timestamps. Empty ID creates a command;
// existing IDs may only belong to the project being edited.
type CommandDraft struct {
	ID            domain.ID                   `json:"id"`
	Configuration domain.CommandConfiguration `json:"configuration"`
}

type RegistrationDraft struct {
	ID          domain.ID         `json:"id"`
	Revision    storage.Revision  `json:"revision"`
	Name        string            `json:"name"`
	Path        string            `json:"path"`
	Commands    []CommandDraft    `json:"commands"`
	Preferences *PreferencesDraft `json:"preferences,omitempty"`
}

type PreferencesDraft struct {
	PreviewURL string                `json:"previewUrl"`
	Editor     *domain.CommandAction `json:"editor,omitempty"`
}

type RegistrationReview struct {
	Token     string         `json:"token"`
	ExpiresAt time.Time      `json:"expiresAt"`
	Project   domain.Project `json:"project"`
	Editing   bool           `json:"editing"`
}

type ReviewResult struct {
	Review  *RegistrationReview `json:"review"`
	Code    string              `json:"code"`
	Message string              `json:"message"`
}

type DirectoryResult struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type fileIdentity struct {
	Path              string
	Device, Inode     uint64
	Mode              uint32
	Size              int64
	Modified, Changed syscall.Timespec
}

type pendingReview struct {
	review     RegistrationReview
	revision   storage.Revision
	identities []fileIdentity
	result     *RegistryResult
}

var (
	errDraft       = errors.New("invalid project configuration")
	errDirectory   = errors.New("project directory unavailable")
	errExecutable  = errors.New("executable unavailable")
	errDuplicate   = errors.New("project directory already registered")
	errReview      = errors.New("review expired or changed")
	errSettings    = errors.New("invalid project settings")
	errEnvironment = errors.New("unsafe environment reference")
)

func (service *Service) BrowseDirectory(ctx context.Context) DirectoryResult {
	if service.picker == nil {
		return DirectoryResult{Code: "unavailable", Message: "The native directory chooser is unavailable. Enter an absolute directory path."}
	}
	select {
	case service.pickerSlot <- struct{}{}:
		defer func() { <-service.pickerSlot }()
	default:
		return DirectoryResult{Code: "busy", Message: "A directory chooser is already open."}
	}
	if ctx.Err() != nil {
		return DirectoryResult{Code: "cancelled"}
	}
	path, err := service.picker()
	if ctx.Err() != nil || err == nil && path == "" {
		return DirectoryResult{Code: "cancelled"}
	}
	if err != nil {
		return DirectoryResult{Code: "chooser", Message: "Could not open the directory chooser. Enter an absolute directory path."}
	}
	return DirectoryResult{Path: path}
}

func (service *Service) openRegistry() error {
	if service.closed {
		return storage.ErrClosed
	}
	if service.registry != nil {
		return nil
	}
	paths, err := service.paths()
	if err != nil {
		return err
	}
	operations, err := storage.OpenPrivateDirectory(filepath.Join(paths.ConfigDir, "project-operations"))
	if err != nil {
		return err
	}
	codec, err := storage.NewProjectCodec()
	if err != nil {
		_ = operations.Close()
		return err
	}
	registry, err := storage.OpenRepository(paths.ProjectsDir(), codec)
	if err != nil {
		_ = operations.Close()
		return err
	}
	service.registry, service.operations = registry, operations
	service.resolvedPaths = paths
	return nil
}

func (service *Service) ServiceShutdown() error {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.closed = true
	clear(service.reviews)
	if service.registry == nil {
		return nil
	}
	err := errors.Join(service.registry.Close(), service.operations.Close())
	service.registry, service.operations = nil, nil
	return err
}

func (service *Service) registryState(ctx context.Context) (*RegistryState, error) {
	records, err := service.registry.List(ctx)
	if err != nil {
		return nil, err
	}
	state := &RegistryState{Projects: make([]ProjectView, 0, len(records))}
	for _, record := range records {
		state.Projects = append(state.Projects, ProjectView{Project: record.Value, Revision: record.Revision})
	}
	slices.SortFunc(state.Projects, func(a, b ProjectView) int {
		if order := strings.Compare(strings.ToLower(a.Project.Name), strings.ToLower(b.Project.Name)); order != 0 {
			return order
		}
		return strings.Compare(string(a.Project.ID), string(b.Project.ID))
	})
	return state, nil
}

func (service *Service) ListProjects(ctx context.Context) RegistryResult {
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.openRegistry(); err != nil {
		return registryFailure(err)
	}
	state, err := service.registryState(ctx)
	if err != nil {
		return registryFailure(err)
	}
	return RegistryResult{State: state}
}

func canonicalDirectory(path string) (string, error) {
	if len(path) > 4096 || !utf8.ValidString(path) || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		return "", errDirectory
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical == "/" {
		return "", errDirectory
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", errDirectory
	}
	return canonical, nil
}

// Directory mtimes are intentionally excluded: normal source edits do not
// change a directory's identity. Executable replacement does invalidate review.
func identity(path string, executable bool) (fileIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return fileIdentity{}, errReview
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSymlink != 0 {
		return fileIdentity{}, errReview
	}
	id := fileIdentity{Path: path, Device: uint64(stat.Dev), Inode: stat.Ino, Mode: stat.Mode}
	if executable {
		if !info.Mode().IsRegular() || unix.Access(path, unix.X_OK) != nil {
			return fileIdentity{}, errExecutable
		}
		id.Size, id.Modified, id.Changed = stat.Size, stat.Mtim, stat.Ctim
	} else if !info.IsDir() {
		return fileIdentity{}, errDirectory
	}
	return id, nil
}

func commandIdentities(project domain.Project) ([]fileIdentity, error) {
	root, err := canonicalDirectory(project.Path)
	if err != nil || root != project.Path {
		return nil, errReview
	}
	first, err := identity(root, false)
	if err != nil {
		return nil, err
	}
	ids := []fileIdentity{first}
	if err := checkEnvironmentReferences(project); err != nil {
		return nil, err
	}
	for _, configuration := range projectActions(project) {
		cwd, err := canonicalDirectory(configuration.WorkingDirectory)
		if err != nil || cwd != configuration.WorkingDirectory {
			return nil, errReview
		}
		directory, err := identity(cwd, false)
		if err != nil {
			return nil, err
		}
		program, err := filepath.EvalSymlinks(configuration.Executable)
		if err != nil || program != configuration.Executable {
			return nil, errReview
		}
		executable, err := identity(program, true)
		if err != nil {
			return nil, err
		}
		ids = append(ids, directory, executable)
	}
	return ids, nil
}

func projectActions(project domain.Project) []domain.CommandAction {
	actions := make([]domain.CommandAction, 0, len(project.Commands)*2+1)
	for _, command := range project.Commands {
		configuration := command.Configuration
		actions = append(actions, domain.CommandAction{Executable: configuration.Executable, Arguments: configuration.Arguments, WorkingDirectory: configuration.WorkingDirectory})
		if configuration.Runtime != nil && configuration.Runtime.Stop != nil && configuration.Runtime.Stop.Action != nil {
			actions = append(actions, *configuration.Runtime.Stop.Action)
		}
	}
	if project.Preferences != nil && project.Preferences.Editor != nil {
		actions = append(actions, *project.Preferences.Editor)
	}
	return actions
}

// O_PATH inspects metadata without reading secrets or opening devices/FIFOs for
// data. Missing files may be configured for later creation; symlink components,
// special files and paths outside the root are never accepted.
func checkEnvironmentReferences(project domain.Project) error {
	root, err := unix.Open(project.Path, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errEnvironment
	}
	defer unix.Close(root)
	for _, command := range project.Commands {
		if command.Configuration.Runtime == nil {
			continue
		}
		for _, reference := range command.Configuration.Runtime.EnvironmentFiles {
			fd, err := unix.Openat2(root, reference, &unix.OpenHow{Flags: unix.O_PATH | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
			if errors.Is(err, unix.ENOENT) {
				continue
			}
			if err != nil {
				return errEnvironment
			}
			var stat unix.Stat_t
			err = unix.Fstat(fd, &stat)
			_ = unix.Close(fd)
			if err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG {
				return errEnvironment
			}
		}
	}
	return nil
}

func normalizeAction(action domain.CommandAction, path string) (domain.CommandAction, error) {
	if len(action.Arguments) > 128 || len(action.Executable) > 4096 {
		return action, errSettings
	}
	action.Arguments = append([]string{}, action.Arguments...)
	if action.WorkingDirectory == "" {
		action.WorkingDirectory = path
	}
	directory, err := canonicalDirectory(action.WorkingDirectory)
	if err != nil {
		return action, errDirectory
	}
	relative, err := filepath.Rel(path, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return action, errDirectory
	}
	action.WorkingDirectory = directory
	if !filepath.IsAbs(action.Executable) {
		if strings.ContainsRune(action.Executable, '/') {
			return action, errExecutable
		}
		action.Executable, err = exec.LookPath(action.Executable)
		if err != nil {
			return action, errExecutable
		}
	}
	action.Executable, err = filepath.EvalSymlinks(action.Executable)
	if err != nil {
		return action, errExecutable
	}
	if action.Validate() != nil {
		return action, errSettings
	}
	return action, nil
}

func (service *Service) normalize(ctx context.Context, draft RegistrationDraft) (domain.Project, error) {
	if len(draft.Commands) > 16 {
		return domain.Project{}, errDraft
	}
	path, err := canonicalDirectory(draft.Path)
	if err != nil {
		return domain.Project{}, err
	}
	now := service.now().UTC()
	project := domain.Project{Name: strings.TrimSpace(draft.Name), Path: path, Source: domain.SourceImported, CreatedAt: now, UpdatedAt: now}
	if draft.Preferences != nil {
		project.Preferences = &domain.ProjectPreferences{PreviewURL: draft.Preferences.PreviewURL}
		if draft.Preferences.Editor != nil {
			action, err := normalizeAction(*draft.Preferences.Editor, path)
			if err != nil {
				return project, err
			}
			project.Preferences.Editor = &action
		}
		if err := project.Preferences.Validate(); err != nil {
			return project, errSettings
		}
		if project.Preferences.PreviewURL == "" && project.Preferences.Editor == nil {
			project.Preferences = nil
		}
	}
	existing := make(map[domain.ID]bool)
	if draft.ID != "" {
		if draft.ID.Validate() != nil || !validRevision(draft.Revision) {
			return project, errDraft
		}
		current, err := service.registry.Get(ctx, draft.ID)
		if err != nil {
			return project, err
		}
		if current.Revision != draft.Revision {
			return project, storage.ErrConflict
		}
		if current.Value.Path != path {
			return project, errDraft
		}
		project.ID, project.CreatedAt, project.Source = current.Value.ID, current.Value.CreatedAt, current.Value.Source
		if project.UpdatedAt.Before(current.Value.UpdatedAt) {
			project.UpdatedAt = current.Value.UpdatedAt
		}
		for _, command := range current.Value.Commands {
			existing[command.ID] = true
		}
	} else {
		if draft.Revision != "" {
			return project, errDraft
		}
		project.ID, err = domain.NewID()
		if err != nil {
			return project, err
		}
	}
	for _, item := range draft.Commands {
		if len(item.Configuration.Arguments) > 128 {
			return project, errDraft
		}
		if runtime := item.Configuration.Runtime; runtime != nil {
			if len(runtime.EnvironmentFiles) > 16 || runtime.Stop != nil && runtime.Stop.Action != nil && len(runtime.Stop.Action.Arguments) > 128 {
				return project, errSettings
			}
		}
		command := domain.StartupCommand{ID: item.ID, Configuration: item.Configuration}
		command.Configuration.Arguments = append([]string{}, item.Configuration.Arguments...)
		command.Configuration.Runtime = domain.CloneRuntime(item.Configuration.Runtime)
		if item.ID == "" {
			command.ID, err = domain.NewID()
			if err != nil {
				return project, err
			}
		} else if !existing[item.ID] {
			return project, errDraft
		}
		configuration := &command.Configuration
		configuration.Name = strings.TrimSpace(configuration.Name)
		action, err := normalizeAction(domain.CommandAction{Executable: configuration.Executable, Arguments: configuration.Arguments, WorkingDirectory: configuration.WorkingDirectory}, path)
		if err != nil {
			if errors.Is(err, errSettings) {
				return project, errDraft
			}
			return project, err
		}
		configuration.Executable, configuration.Arguments, configuration.WorkingDirectory = action.Executable, action.Arguments, action.WorkingDirectory
		if configuration.Runtime != nil {
			if configuration.Runtime.Stop != nil && configuration.Runtime.Stop.Action != nil {
				action, err := normalizeAction(*configuration.Runtime.Stop.Action, path)
				if err != nil {
					return project, err
				}
				configuration.Runtime.Stop.Action = &action
			}
			if err := configuration.Runtime.Validate(); err != nil {
				return project, errSettings
			}
		}
		project.Commands = append(project.Commands, command)
	}
	if project.Validate() != nil {
		return project, errDraft
	}
	return project, nil
}

func validRevision(revision storage.Revision) bool {
	if len(revision) != 64 {
		return false
	}
	for _, r := range revision {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func (service *Service) checkUnique(ctx context.Context, project domain.Project) error {
	records, err := service.registry.List(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Value.ID == project.ID {
			continue
		}
		if record.Value.Path == project.Path {
			return errDuplicate
		}
		// Catch an older registration reached through a newly introduced alias.
		if canonical, err := canonicalDirectory(record.Value.Path); err == nil && canonical == project.Path {
			return errDuplicate
		}
	}
	return nil
}

func cloneProject(project domain.Project) domain.Project {
	project.Commands = domain.CloneCommands(project.Commands)
	project.Preferences = domain.ClonePreferences(project.Preferences)
	return project
}

func (service *Service) pruneReviews() {
	for token, pending := range service.reviews {
		if !service.now().Before(pending.review.ExpiresAt) {
			delete(service.reviews, token)
		}
	}
}

func (service *Service) ReviewRegistration(ctx context.Context, draft RegistrationDraft) ReviewResult {
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.openRegistry(); err != nil {
		failure := registryFailure(err)
		return ReviewResult{Code: failure.Code, Message: failure.Message}
	}
	service.pruneReviews()
	if len(service.reviews) >= 64 {
		return ReviewResult{Code: "busy", Message: "Too many recent reviews. Wait five minutes, then review again."}
	}
	var review RegistrationReview
	err := storage.WithDirectoryLock(ctx, service.operations, func() error {
		project, err := service.normalize(ctx, draft)
		if err != nil {
			return err
		}
		if err := service.checkUnique(ctx, project); err != nil {
			return err
		}
		codec, err := storage.NewProjectCodec()
		if err != nil {
			return err
		}
		if _, err := codec.Encode(project); err != nil {
			return errSettings
		}
		identities, err := commandIdentities(project)
		if err != nil {
			return err
		}
		token, err := domain.NewID()
		if err != nil {
			return err
		}
		review = RegistrationReview{Token: string(token), ExpiresAt: service.now().Add(5 * time.Minute).UTC(), Project: project, Editing: draft.ID != ""}
		service.reviews[review.Token] = &pendingReview{review: review, revision: draft.Revision, identities: identities}
		return nil
	})
	if err != nil {
		failure := registryFailure(err)
		return ReviewResult{Code: failure.Code, Message: failure.Message}
	}
	review.Project = cloneProject(review.Project)
	return ReviewResult{Review: &review}
}

// CommitRegistration accepts only a server-held review token, never new argv.
// Repeating a token returns its cached outcome and cannot perform a second write.
func (service *Service) CommitRegistration(ctx context.Context, token string) RegistryResult {
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.openRegistry(); err != nil {
		return registryFailure(err)
	}
	service.pruneReviews()
	pending := service.reviews[token]
	if pending == nil {
		return registryFailure(errReview)
	}
	if pending.result != nil {
		return cloneResult(*pending.result)
	}
	committed := false
	err := storage.WithDirectoryLock(ctx, service.operations, func() error {
		if !service.now().Before(pending.review.ExpiresAt) {
			return errReview
		}
		project := cloneProject(pending.review.Project)
		identities, err := commandIdentities(project)
		if err != nil || !slices.Equal(identities, pending.identities) {
			return errReview
		}
		if err := service.checkUnique(ctx, project); err != nil {
			return err
		}
		approvedAt := service.now().UTC()
		if project.Preferences != nil {
			project.Preferences.Approval = &domain.CommandApproval{Fingerprint: project.Preferences.Fingerprint(), ApprovedAt: approvedAt}
		}
		for i := range project.Commands {
			project.Commands[i].Approval = &domain.CommandApproval{Fingerprint: project.Commands[i].Configuration.Fingerprint(), ApprovedAt: approvedAt}
		}
		if approvedAt.After(project.UpdatedAt) {
			project.UpdatedAt = approvedAt
		}
		// Approval metadata adds bytes beyond the unsigned review. Check the final
		// document budget before entering a mutation, and keep oversize drafts editable.
		codec, err := storage.NewProjectCodec()
		if err != nil {
			return err
		}
		if _, err := codec.Encode(project); err != nil {
			return errSettings
		}
		if pending.review.Editing {
			err = service.registry.Update(ctx, project.ID, pending.revision, func(domain.Project) (domain.Project, error) { return project, nil })
		} else {
			err = service.registry.Create(ctx, project)
		}
		if err == nil {
			committed = true
		}
		return err
	})
	result := RegistryResult{Committed: committed}
	if err != nil {
		result = registryFailure(err)
	} else {
		result.State, err = service.registryState(ctx)
		if err != nil {
			result.Code, result.Message = "refresh_required", "Project saved, but the list could not refresh. Reload registered projects before editing again."
		}
	}
	pending.result = &result
	return cloneResult(result)
}

func cloneResult(result RegistryResult) RegistryResult {
	if result.State != nil {
		state := &RegistryState{Projects: slices.Clone(result.State.Projects)}
		for i := range state.Projects {
			state.Projects[i].Project = cloneProject(state.Projects[i].Project)
		}
		result.State = state
	}
	return result
}

func registryFailure(err error) RegistryResult {
	result := RegistryResult{Code: "storage", Message: "Could not access project storage. Check XDG paths and private directory permissions, then reload registered projects."}
	var mutation *storage.MutationError
	if errors.As(err, &mutation) && mutation.Committed {
		return RegistryResult{Committed: true, Code: "durability_uncertain", Message: "The project change took effect, but durability could not be confirmed. Reload registered projects before editing again."}
	}
	switch {
	case errors.Is(err, errSettings):
		result.Code, result.Message = "settings", "Check project settings: use valid HTTP(S) URLs without credentials, loopback readiness with bounded timing, project-relative environment references, valid editor/stop argv, SIGTERM or SIGINT with a 1–120 second grace period, and a 0–3600 second delay."
	case errors.Is(err, errEnvironment):
		result.Code, result.Message = "environment_reference", "An environment reference is unsafe or inaccessible. Use a regular file inside the project with no symlink components, or a missing project-relative path to create later. File contents were not read."
	case errors.Is(err, errDraft):
		result.Code, result.Message = "validation", "Use a name of 1–120 characters and up to 16 distinct commands, each with a name and up to 128 literal arguments (32 KiB total). An existing project's directory cannot be changed."
	case errors.Is(err, errDirectory):
		result.Code, result.Message = "directory", "Choose an existing project directory below filesystem root. Working directories must be the project directory or one of its subdirectories."
	case errors.Is(err, errExecutable):
		result.Code, result.Message = "executable", "A startup executable is unavailable or not executable. Use a tool on PATH or an absolute executable path, or register without startup commands."
	case errors.Is(err, errDuplicate):
		result.Code, result.Message = "duplicate", "This canonical directory is already registered. Reload registered projects and edit its configuration."
	case errors.Is(err, errReview):
		result.Code, result.Message = "review_changed", "The review expired or a directory or executable changed. Return to the configuration and review again."
	case errors.Is(err, storage.ErrConflict):
		result.Code, result.Message = "conflict", "This project changed in another window. Keep your draft, reload registered projects, then reopen the latest configuration."
	case errors.Is(err, storage.ErrUnsupportedSchema):
		result.Code, result.Message = "newer_schema", "A project uses an unsupported schema. Open it with a compatible application version; its bytes were preserved."
	case errors.Is(err, storage.ErrInvalidDocument), errors.Is(err, storage.ErrWrongKind), errors.Is(err, storage.ErrDocumentTooLarge):
		result.Code, result.Message = "invalid_storage", "A project configuration could not be read or exceeds storage limits. Existing bytes were preserved; repair or recover the record before continuing."
	case errors.Is(err, fs.ErrNotExist):
		result.Code, result.Message = "not_found", "This project no longer exists. Reload registered projects."
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		result.Code, result.Message = "cancelled", "The operation was cancelled. Reload registered projects to check the saved state."
	case errors.Is(err, storage.ErrClosed):
		result.Code, result.Message = "closed", "The desktop service has stopped. Reopen the application."
	}
	return result
}
