// Package projects exposes project inspection independently of the WebView.
package projects

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/projects/bootstrap"
	"workspace-manager/internal/projects/detection"
	"workspace-manager/internal/storage"
)

type Result struct {
	Report  *detection.Report `json:"report"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
}

// Service separates inspection from explicit reviewed registration. It has no
// project execution dependency. Only explicitly reviewed official scaffolding tools
// and setup tools can run. Startup commands never execute here.
type Service struct {
	inspector        detection.Inspector
	slots            chan struct{}
	mu               sync.Mutex
	paths            func() (storage.Paths, error)
	resolvedPaths    storage.Paths
	registry         *storage.Repository[domain.Project]
	operations       *os.Root
	reviews          map[string]*pendingReview
	creations        map[string]*pendingCreation
	setups           map[string]*pendingSetup
	recoveryReviews  map[string]*pendingRecoveryRegistration
	now              func() time.Time
	picker           func() (string, error)
	pickerSlot       chan struct{}
	closed           bool
	scaffolder       func(context.Context, domain.CommandAction, string) ([]bootstrap.File, bootstrap.CommandResult, error)
	preparationSlot  chan struct{}
	setupRunner      func(context.Context, domain.CommandAction, string, *os.File) bootstrap.CommandResult
	versionRunner    func(context.Context, domain.CommandAction, string) bootstrap.CommandResult
	versionCancel    context.CancelFunc
	logStart         func(context.Context, storage.Paths, domain.ID, SetupReview, time.Time) (setupLogWriter, error)
	creationLogStart func(context.Context, storage.Paths, domain.CreationLog) (creationLogWriter, error)
}

type Option func(*Service)

func WithDirectoryPicker(picker func() (string, error)) Option {
	return func(service *Service) { service.picker = picker }
}

func NewService(inspector detection.Inspector, options ...Option) *Service {
	service := &Service{inspector: inspector, slots: make(chan struct{}, 2), paths: storage.ResolvePaths, now: time.Now, reviews: make(map[string]*pendingReview), pickerSlot: make(chan struct{}, 1), preparationSlot: make(chan struct{}, 1), scaffolder: bootstrap.PrepareScaffoldLogged, setupRunner: bootstrap.RunSetup, setups: make(map[string]*pendingSetup)}
	service.versionRunner = bootstrap.RunVersion
	service.logStart = startSetupLog
	service.creationLogStart = startCreationLog
	for _, option := range options {
		option(service)
	}
	return service
}

func (service *Service) Inspect(ctx context.Context, path string) Result {
	select {
	case service.slots <- struct{}{}:
		defer func() { <-service.slots }()
	default:
		return Result{Code: "busy", Message: "Two inspections are already in progress. Wait for them to finish, then try again."}
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	report, err := service.inspector.Inspect(ctx, path)
	if err == nil {
		err = ctx.Err()
	}
	switch {
	case errors.Is(err, context.Canceled):
		return Result{Code: "cancelled", Message: "Inspection cancelled. No project configuration was saved."}
	case errors.Is(err, context.DeadlineExceeded):
		return Result{Code: "timeout", Message: "Inspection exceeded its time budget. Inspect a smaller directory and try again."}
	case errors.Is(err, detection.ErrDirectory):
		return Result{Code: "directory", Message: "Could not open that project directory. Use an existing absolute directory path and check its permissions."}
	case err != nil:
		return Result{Code: "inspection_failed", Message: "Could not inspect this directory. No project configuration was saved. Check access and try again."}
	default:
		return Result{Report: &report}
	}
}
