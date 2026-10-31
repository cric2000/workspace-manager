package applications

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"time"

	"workspace-manager/internal/domain"
)

var ErrLaunchChanged = errors.New("desktop launch details changed")

// LaunchSpec stays in Go. Fingerprint covers both registered metadata and the
// desktop file; clients cannot supply an executable, arguments or fingerprint.
type LaunchSpec struct {
	Entry       DesktopEntry
	Fingerprint string
}

type Launcher interface {
	Review(context.Context, string) (LaunchSpec, error)
	// Launch returns nil for an accepted handoff, not readiness. Context errors
	// must mean cancellation before dispatch; an accepted launch is not retractable.
	Launch(context.Context, LaunchSpec) error
}

type ReviewResult struct {
	Token    string                    `json:"token"`
	Entry    *DesktopEntry             `json:"entry"`
	Code     string                    `json:"code"`
	Message  string                    `json:"message"`
	Custom   *domain.CustomApplication `json:"custom,omitempty"`
	Policy   domain.DuplicatePolicy    `json:"policy"`
	Instance InstanceStatus            `json:"instance"`
}

type LaunchResult struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type launchRecord struct {
	spec    LaunchSpec
	expires time.Time
	started bool
	result  LaunchResult
	custom  *CustomLaunchSpec
	target  CustomTarget
	policy  domain.DuplicatePolicy
	busName string
}

// LaunchService owns bounded, short-lived approvals for this GUI process only.
// Repeating a token returns its outcome; it never repeats a launch attempt.
// These are desktop handoffs, not supervised processes or workspace executions.
type LaunchService struct {
	launcher       Launcher
	mu             sync.Mutex
	records        map[string]*launchRecord
	now            func() time.Time
	customLauncher CustomLauncher
	resolveCustom  CustomResolver
	instances      InstanceChecker
}

func NewLaunchService(launcher Launcher, options ...LaunchOption) *LaunchService {
	service := &LaunchService{launcher: launcher, records: make(map[string]*launchRecord), now: time.Now}
	for _, option := range options {
		option(service)
	}
	return service
}

func (service *LaunchService) Review(ctx context.Context, id string) ReviewResult {
	return service.ReviewDesktop(ctx, id, domain.AlwaysLaunch)
}

func (service *LaunchService) ReviewDesktop(ctx context.Context, id string, policy domain.DuplicatePolicy) ReviewResult {
	if ctx.Err() != nil || ValidateDesktopID(id) != nil || !validPolicy(policy) {
		return ReviewResult{Code: "unavailable", Message: "This application is unavailable. Refresh the catalogue and try again."}
	}
	spec, err := service.launcher.Review(ctx, id)
	if err != nil || ctx.Err() != nil || spec.Entry.ID != id || !spec.Entry.Visible || spec.Fingerprint == "" {
		return ReviewResult{Code: "unavailable", Message: "Could not review this application. Refresh the catalogue and check its installation."}
	}
	entry := spec.Entry
	return service.issue(ctx, &launchRecord{spec: spec, policy: policy, busName: desktopBusName(entry)}, ReviewResult{Entry: &entry, Policy: policy})
}

func (service *LaunchService) issue(ctx context.Context, record *launchRecord, result ReviewResult) ReviewResult {
	result.Instance = service.instance(ctx, record.busName)
	if ctx.Err() != nil {
		return ReviewResult{Code: "cancelled", Message: "Review cancelled. Try again."}
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	now := service.now()
	for token, record := range service.records {
		if !now.Before(record.expires) && record.result.Code != "in_progress" {
			delete(service.records, token)
		}
	}
	if len(service.records) >= 64 {
		return ReviewResult{Code: "busy", Message: "Too many recent launch reviews. Wait five minutes before reviewing another application."}
	}
	token := rand.Text()
	record.expires = now.Add(5 * time.Minute)
	service.records[token] = record
	result.Token = token
	return result
}

func (service *LaunchService) Launch(ctx context.Context, token string) LaunchResult {
	service.mu.Lock()
	record := service.records[token]
	if record == nil {
		service.mu.Unlock()
		return LaunchResult{Code: "review_required", Message: "This review is no longer available. Check whether the application opened before reviewing again."}
	}
	if record.started {
		result := record.result
		service.mu.Unlock()
		return result
	}
	if !service.now().Before(record.expires) {
		delete(service.records, token)
		service.mu.Unlock()
		return LaunchResult{Code: "review_required", Message: "This review expired. Review the current launch details again."}
	}
	record.started = true
	record.result = LaunchResult{Code: "in_progress", Message: "The launch request is still being processed. Check its result shortly."}
	service.mu.Unlock()

	var result LaunchResult
	err := ctx.Err()
	if err == nil {
		err = service.preflight(ctx, record)
	}
	if err == nil && record.policy == domain.SkipIfRunning {
		instance := service.instance(ctx, record.busName)
		switch instance.State {
		case "running":
			result = LaunchResult{Code: "skipped", Message: "Skipped: the configured D-Bus identity currently has an owner. No launch was requested; window readiness is not verified."}
		case "unknown":
			result = LaunchResult{Code: "instance_unknown", Message: "No launch was requested because running state could not be confirmed. Review again with Always request a launch if you want to proceed anyway."}
		}
	}
	if err == nil && result.Code == "" {
		if record.custom != nil {
			err = service.customLauncher.LaunchCustom(ctx, *record.custom)
		} else {
			err = service.launcher.Launch(ctx, record.spec)
		}
	}
	switch {
	case result.Code != "":
	case err == nil:
		result = LaunchResult{Code: "requested", Message: "Launch request accepted by the desktop. Check the application window; readiness is not verified."}
		if record.custom != nil {
			result.Message = "Launch request accepted. Check the application window; readiness is not verified."
		}
	case errors.Is(err, ErrLaunchChanged):
		result = LaunchResult{Code: "changed", Message: "The desktop entry changed after review. Review its current details before launching."}
		if record.custom != nil {
			result.Message = "The saved custom configuration or executable changed after review. Refresh saved tools and review the current configuration again."
		}
	case errors.Is(err, ErrUnavailable):
		result = LaunchResult{Code: "unavailable", Message: "This application is no longer available. Refresh the catalogue and check its installation."}
		if record.custom != nil {
			result.Message = "The custom executable or working directory is unavailable. Check its paths and permissions, then refresh saved tools and review again."
		}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		result = LaunchResult{Code: "cancelled", Message: "The request was cancelled before launch. Review again to retry."}
	default:
		result = LaunchResult{Code: "failed", Message: "The desktop could not accept the launch request. Check whether the application opened and check its installation or terminal configuration before reviewing again."}
		if record.custom != nil {
			result.Message = "The custom executable could not start. Check whether it opened, then check its execute permissions and working directory before reviewing again."
		}
	}
	service.mu.Lock()
	record.result = result
	service.mu.Unlock()
	return result
}

func (service *LaunchService) preflight(ctx context.Context, record *launchRecord) error {
	if record.custom != nil {
		custom, err := service.resolveCustom(ctx, record.target)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrLaunchChanged
		}
		current, err := service.customLauncher.ReviewCustom(ctx, custom)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrUnavailable
		}
		if current.Fingerprint != record.custom.Fingerprint {
			return ErrLaunchChanged
		}
	} else {
		current, err := service.launcher.Review(ctx, record.spec.Entry.ID)
		if err != nil {
			return err
		}
		if current.Fingerprint != record.spec.Fingerprint {
			return ErrLaunchChanged
		}
	}
	return ctx.Err()
}
