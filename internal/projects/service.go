// Package projects exposes project inspection independently of the WebView.
package projects

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/projects/detection"
	"workspace-manager/internal/storage"
)

type Result struct {
	Report  *detection.Report `json:"report"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
}

// Service separates inspection from explicit reviewed registration. It has no
// execution dependency: none of its methods can start a project command.
type Service struct {
	inspector     detection.Inspector
	slots         chan struct{}
	mu            sync.Mutex
	paths         func() (storage.Paths, error)
	resolvedPaths storage.Paths
	registry      *storage.Repository[domain.Project]
	operations    *os.Root
	reviews       map[string]*pendingReview
	now           func() time.Time
	picker        func() (string, error)
	pickerSlot    chan struct{}
	closed        bool
}

type Option func(*Service)

func WithDirectoryPicker(picker func() (string, error)) Option {
	return func(service *Service) { service.picker = picker }
}

func NewService(inspector detection.Inspector, options ...Option) *Service {
	service := &Service{inspector: inspector, slots: make(chan struct{}, 2), paths: storage.ResolvePaths, now: time.Now, reviews: make(map[string]*pendingReview), pickerSlot: make(chan struct{}, 1)}
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
