// Package workspaces implements profile editing, independently of the GUI runtime.
package workspaces

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"workspace-manager/internal/applications"
	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

// Draft deliberately excludes identity, timestamps and project references.
type Draft struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Target struct {
	ID       domain.ID        `json:"id"`
	Revision storage.Revision `json:"revision"`
}

type View struct {
	ID           domain.ID                     `json:"id"`
	Name         string                        `json:"name"`
	Description  string                        `json:"description"`
	ProjectCount int                           `json:"projectCount"`
	Applications []domain.WorkspaceApplication `json:"applications"`
	CreatedAt    time.Time                     `json:"createdAt"`
	UpdatedAt    time.Time                     `json:"updatedAt"`
	Revision     storage.Revision              `json:"revision"`
}

type State struct {
	Workspaces         []View           `json:"workspaces"`
	DefaultWorkspaceID domain.ID        `json:"defaultWorkspaceId"`
	SettingsRevision   storage.Revision `json:"settingsRevision"`
	DefaultMissing     bool             `json:"defaultMissing"`
}

// Result contains only safe user-facing messages, never filesystem error text.
// Committed means a mutation took effect, even if refresh/durability failed.
// Such a mutation must be reconciled by List, not blindly retried.
type Result struct {
	State     *State `json:"state"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Committed bool   `json:"committed"`
}

var (
	errValidation          = errors.New("invalid workspace input")
	errDefault             = errors.New("workspace is the default")
	errRequiredDefault     = errors.New("autostart requires a default")
	errApplication         = errors.New("new application is unavailable")
	errApplicationSettings = errors.New("invalid application settings")
)

type ApplicationCatalog interface {
	Lookup(context.Context, string) (applications.DesktopEntry, error)
}

// Service opens storage lazily so an inaccessible configuration leaves the GUI
// available with a retry action. The mutex protects initialization and shutdown;
// the directory lock also coordinates separate GUI processes.
type Service struct {
	mu         sync.Mutex
	paths      func() (storage.Paths, error)
	profiles   *storage.Repository[domain.Workspace]
	settings   *storage.SettingsRepository
	operations *os.Root
	closed     bool
	catalog    ApplicationCatalog
}

func NewService(catalog ApplicationCatalog) *Service {
	return &Service{paths: storage.ResolvePaths, catalog: catalog}
}

func (service *Service) open() error {
	if service.closed {
		return storage.ErrClosed
	}
	if service.operations != nil {
		return nil
	}
	paths, err := service.paths()
	if err != nil {
		return err
	}
	operations, err := storage.OpenPrivateDirectory(filepath.Join(paths.ConfigDir, "workspace-operations"))
	if err != nil {
		return err
	}
	workspaceCodec, err := storage.NewWorkspaceCodec()
	if err != nil {
		_ = operations.Close()
		return err
	}
	profiles, err := storage.OpenRepository(paths.WorkspacesDir(), workspaceCodec)
	if err != nil {
		_ = operations.Close()
		return err
	}
	settingsCodec, err := storage.NewCodec[domain.Settings](storage.InitialSchemaVersion, nil)
	if err != nil {
		_ = profiles.Close()
		_ = operations.Close()
		return err
	}
	settings, err := storage.OpenSettingsRepository(paths.ConfigDir, settingsCodec)
	if err != nil {
		_ = profiles.Close()
		_ = operations.Close()
		return err
	}
	service.profiles, service.settings, service.operations = profiles, settings, operations
	return nil
}

// ServiceShutdown is recognized by Wails and excluded from generated bindings.
func (service *Service) ServiceShutdown() error {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		return nil
	}
	service.closed = true
	if service.operations == nil {
		return nil
	}
	return errors.Join(service.profiles.Close(), service.settings.Close(), service.operations.Close())
}

func (service *Service) read(ctx context.Context) (*State, error) {
	settings, err := service.settings.Get(ctx)
	if errors.Is(err, fs.ErrNotExist) {
		// Only absence permits defaults. Corrupt/newer files remain untouched.
		if err = service.settings.Create(ctx, domain.DefaultSettings()); err != nil {
			return nil, err
		}
		settings, err = service.settings.Get(ctx)
	}
	if err != nil {
		return nil, err
	}
	profiles, err := service.profiles.List(ctx)
	if err != nil {
		return nil, err
	}
	state := &State{Workspaces: make([]View, 0, len(profiles)), DefaultWorkspaceID: settings.Value.DefaultWorkspaceID, SettingsRevision: settings.Revision}
	state.DefaultMissing = state.DefaultWorkspaceID != ""
	for _, profile := range profiles {
		value := profile.Value
		state.Workspaces = append(state.Workspaces, View{ID: value.ID, Name: value.Name, Description: value.Description, ProjectCount: len(value.ProjectIDs), Applications: domain.CloneApplications(value.Applications), CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, Revision: profile.Revision})
		if value.ID == state.DefaultWorkspaceID {
			state.DefaultMissing = false
		}
	}
	slices.SortFunc(state.Workspaces, func(a, b View) int {
		if order := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); order != 0 {
			return order
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return state, nil
}

func (service *Service) perform(ctx context.Context, mutation func(*State) error) Result {
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.open(); err != nil {
		return failure(err)
	}
	var result Result
	mutationStarted := false
	err := storage.WithDirectoryLock(ctx, service.operations, func() error {
		state, err := service.read(ctx)
		if err != nil {
			return err
		}
		if mutation == nil {
			result.State = state
			return nil
		}
		mutationStarted = true
		if err := mutation(state); err != nil {
			return err
		}
		result.Committed = true
		result.State, err = service.read(ctx)
		if err != nil {
			result.Code, result.Message = "refresh_required", "The change was saved, but the list could not refresh. Reload workspaces before making another change."
		}
		return nil
	})
	if err != nil {
		result = failure(err)
		if result.Committed && !mutationStarted {
			// Only initial settings creation can write during read. Do not tell a
			// caller that its requested profile mutation was already performed.
			result.Committed = false
			result.Code, result.Message = "initialization_uncertain", "Local preferences were initialized, but their durability could not be confirmed. Reload workspaces before continuing; the requested profile change was not started."
		}
		return result
	}
	return result
}

func (service *Service) List(ctx context.Context) Result { return service.perform(ctx, nil) }

func draftWorkspace(draft Draft) (domain.Workspace, error) {
	id, err := domain.NewID()
	if err != nil {
		return domain.Workspace{}, err
	}
	now := time.Now().UTC()
	value := domain.Workspace{ID: id, Name: strings.TrimSpace(draft.Name), Description: draft.Description, ProjectIDs: []domain.ID{}, Applications: []domain.WorkspaceApplication{}, CreatedAt: now, UpdatedAt: now}
	if err := value.Validate(); err != nil {
		return domain.Workspace{}, errValidation
	}
	return value, nil
}

func validTarget(target Target) error {
	if err := target.ID.Validate(); err != nil {
		return errValidation
	}
	if len(target.Revision) != 64 {
		return errValidation
	}
	for _, r := range target.Revision {
		if !(r >= 'a' && r <= 'f' || r >= '0' && r <= '9') {
			return errValidation
		}
	}
	return nil
}

func (service *Service) Create(ctx context.Context, draft Draft) Result {
	return service.perform(ctx, func(_ *State) error {
		value, err := draftWorkspace(draft)
		if err != nil {
			return err
		}
		return service.profiles.Create(ctx, value)
	})
}

func (service *Service) Edit(ctx context.Context, target Target, draft Draft) Result {
	return service.perform(ctx, func(_ *State) error {
		if err := validTarget(target); err != nil {
			return err
		}
		return service.profiles.Update(ctx, target.ID, target.Revision, func(value domain.Workspace) (domain.Workspace, error) {
			value.Name, value.Description = strings.TrimSpace(draft.Name), draft.Description
			// A wall clock adjustment must not move the revision timestamp backwards.
			now := time.Now().UTC()
			if now.After(value.UpdatedAt) {
				value.UpdatedAt = now
			}
			if err := value.Validate(); err != nil {
				return value, errValidation
			}
			return value, nil
		})
	})
}

func (service *Service) Duplicate(ctx context.Context, target Target, draft Draft) Result {
	return service.perform(ctx, func(_ *State) error {
		if err := validTarget(target); err != nil {
			return err
		}
		original, err := service.profiles.Get(ctx, target.ID)
		if err != nil {
			return err
		}
		if original.Revision != target.Revision {
			return storage.ErrConflict
		}
		value, err := draftWorkspace(draft)
		if err != nil {
			return err
		}
		value.ProjectIDs = slices.Clone(original.Value.ProjectIDs)
		value.Applications = domain.CloneApplications(original.Value.Applications)
		return service.profiles.Create(ctx, value)
	})
}

// SetApplications changes only the ordered application configuration. Existing
// unavailable IDs may be retained; new IDs must currently be visible registrations.
// Custom executable configuration is saved without checking availability or
// granting approval. Offline/missing paths remain editable and are checked at review.
func (service *Service) SetApplications(ctx context.Context, target Target, selection []domain.WorkspaceApplication) Result {
	if len(selection) > domain.MaxWorkspaceApplications {
		return failure(errApplicationSettings)
	}
	choices := domain.CloneApplications(selection)
	return service.perform(ctx, func(_ *State) error {
		if err := validTarget(target); err != nil {
			return err
		}
		return service.profiles.Update(ctx, target.ID, target.Revision, func(value domain.Workspace) (domain.Workspace, error) {
			existing := make(map[domain.DesktopID]bool, len(value.Applications))
			existingCustom := make(map[domain.ID]bool)
			for _, application := range value.Applications {
				existing[application.DesktopID] = true
				if application.Custom != nil {
					existingCustom[application.Custom.ID] = true
				}
			}
			for i := range choices {
				if custom := choices[i].Custom; custom != nil {
					if custom.ID != "" {
						if !existingCustom[custom.ID] {
							return value, errApplicationSettings
						}
						continue
					}
					id, err := domain.NewID()
					if err != nil {
						return value, err
					}
					custom.ID = id
				}
			}
			value.Applications = domain.CloneApplications(choices)
			codec, err := storage.NewWorkspaceCodec()
			if err != nil {
				return value, err
			}
			// Validate the encoded budget before any persistence work. JSON escaping
			// can exceed the document limit even when individual argv limits pass.
			if _, err := codec.Encode(value); err != nil {
				return value, errApplicationSettings
			}
			for _, application := range choices {
				if application.Custom != nil {
					continue
				}
				if existing[application.DesktopID] {
					continue
				}
				if service.catalog == nil {
					return value, errApplication
				}
				entry, err := service.catalog.Lookup(ctx, string(application.DesktopID))
				if ctx.Err() != nil {
					return value, ctx.Err()
				}
				if err != nil || !entry.Visible || entry.ID != string(application.DesktopID) {
					return value, errApplication
				}
			}
			if now := time.Now().UTC(); now.After(value.UpdatedAt) {
				value.UpdatedAt = now
			}
			return value, nil
		})
	})
}

func (service *Service) Delete(ctx context.Context, target Target) Result {
	return service.perform(ctx, func(state *State) error {
		if err := validTarget(target); err != nil {
			return err
		}
		if state.DefaultWorkspaceID == target.ID {
			return errDefault
		}
		return service.profiles.Delete(ctx, target.ID, target.Revision)
	})
}

// Empty ID explicitly clears the default. SettingsRevision protects unrelated
// settings changed by another window, and workspace revision protects selection.
func (service *Service) SetDefault(ctx context.Context, target Target, settingsRevision storage.Revision) Result {
	return service.perform(ctx, func(_ *State) error {
		if target.ID != "" {
			if err := validTarget(target); err != nil {
				return err
			}
			current, err := service.profiles.Get(ctx, target.ID)
			if err != nil {
				return err
			}
			if current.Revision != target.Revision {
				return storage.ErrConflict
			}
		}
		return service.settings.Update(ctx, settingsRevision, func(settings domain.Settings) (domain.Settings, error) {
			if target.ID == "" && settings.Autostart && settings.StartupMode != domain.StartupRestore {
				return settings, errRequiredDefault
			}
			settings.DefaultWorkspaceID = target.ID
			return settings, nil
		})
	})
}

func failure(err error) Result {
	result := Result{Code: "storage", Message: "Could not access local storage. Check XDG paths and private directory permissions, then reload workspaces."}
	var mutation *storage.MutationError
	if errors.As(err, &mutation) && mutation.Committed {
		return Result{Code: "durability_uncertain", Committed: true, Message: "The change took effect, but storage durability could not be confirmed. Reload workspaces before making another change."}
	}
	switch {
	case errors.Is(err, errApplication):
		result.Code, result.Message = "application_unavailable", "An added application is no longer available. Refresh the application list and choose an installed application, or remove it from this draft."
	case errors.Is(err, errApplicationSettings):
		result.Code, result.Message = "validation", "Choose up to 64 distinct applications with valid duplicate preferences. Custom tools need a name, absolute paths, up to 128 literal arguments (32 KiB total) and a valid optional D-Bus identity."
	case errors.Is(err, errValidation):
		result.Code, result.Message = "validation", "Enter a name of 1–120 characters without control characters and a description of at most 4,096 characters."
	case errors.Is(err, storage.ErrConflict):
		result.Code, result.Message = "conflict", "This workspace or its settings changed in another window. Keep your draft, reload workspaces, then reopen the editor to review the latest version."
	case errors.Is(err, errDefault):
		result.Code, result.Message = "default_workspace", "Choose another default or clear the default before deleting this workspace."
	case errors.Is(err, errRequiredDefault):
		result.Code, result.Message = "default_required", "The saved login startup mode requires a default workspace. Choose another workspace instead."
	case errors.Is(err, storage.ErrUnsupportedSchema):
		result.Code, result.Message = "newer_schema", "Saved configuration uses an unsupported schema. Open it with a compatible application version; the files have been preserved."
	case errors.Is(err, storage.ErrInvalidDocument), errors.Is(err, storage.ErrWrongKind), errors.Is(err, storage.ErrDocumentTooLarge):
		result.Code, result.Message = "invalid_storage", "A saved configuration could not be read. Its bytes have been preserved. Repair or recover the file before reloading workspaces."
	case errors.Is(err, fs.ErrNotExist):
		result.Code, result.Message = "not_found", "This workspace no longer exists. Reload workspaces to see the current profiles."
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		result.Code, result.Message = "cancelled", "The operation was cancelled. Reload workspaces to check the saved state."
	case errors.Is(err, storage.ErrClosed):
		result.Code, result.Message = "closed", "The desktop service has stopped. Reopen the application."
	}
	return result
}
