package applications

import (
	"context"
	"strings"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

type CustomTarget struct {
	WorkspaceID domain.ID        `json:"workspaceId"`
	Revision    storage.Revision `json:"revision"`
	CustomID    domain.ID        `json:"customId"`
}

type CustomLaunchSpec struct {
	Application domain.CustomApplication
	Fingerprint string
}

type CustomLauncher interface {
	ReviewCustom(context.Context, domain.CustomApplication) (CustomLaunchSpec, error)
	LaunchCustom(context.Context, CustomLaunchSpec) error
}
type CustomResolver func(context.Context, CustomTarget) (domain.CustomApplication, error)

type InstanceStatus struct {
	State   string `json:"state"`
	Message string `json:"message"`
}
type InstanceChecker interface {
	Check(context.Context, string) InstanceStatus
}

type LaunchOption func(*LaunchService)

func WithCustomLauncher(launcher CustomLauncher, resolver CustomResolver) LaunchOption {
	return func(service *LaunchService) { service.customLauncher, service.resolveCustom = launcher, resolver }
}
func WithInstanceChecker(checker InstanceChecker) LaunchOption {
	return func(service *LaunchService) { service.instances = checker }
}

func (service *LaunchService) instance(ctx context.Context, busName string) InstanceStatus {
	if service.instances == nil || !domain.ValidBusName(busName) {
		return InstanceStatus{State: "unknown", Message: "Running state is unknown. No supported D-Bus identity is available; process names are not proof of an instance."}
	}
	result := service.instances.Check(ctx, busName)
	if result.State != "running" && result.State != "not_running" {
		result.State = "unknown"
	}
	return result
}

func desktopBusName(entry DesktopEntry) string {
	if !entry.DBusActivatable {
		return ""
	}
	name := strings.TrimSuffix(entry.ID, ".desktop")
	if !domain.ValidBusName(name) {
		return ""
	}
	return name
}

func validPolicy(policy domain.DuplicatePolicy) bool {
	return policy == domain.AlwaysLaunch || policy == domain.SkipIfRunning
}

func (service *LaunchService) ReviewCustom(ctx context.Context, target CustomTarget, policy domain.DuplicatePolicy) ReviewResult {
	failure := ReviewResult{Code: "unavailable", Message: "Could not review this custom application. Refresh saved tools and check its executable and working-directory paths."}
	if ctx.Err() != nil || !validPolicy(policy) || service.customLauncher == nil || service.resolveCustom == nil {
		return failure
	}
	custom, err := service.resolveCustom(ctx, target)
	if err != nil {
		return failure
	}
	spec, err := service.customLauncher.ReviewCustom(ctx, custom)
	if err != nil || ctx.Err() != nil || spec.Fingerprint == "" {
		return failure
	}
	custom.Arguments = append([]string{}, custom.Arguments...)
	return service.issue(ctx, &launchRecord{custom: &spec, target: target, policy: policy, busName: custom.InstanceBusName}, ReviewResult{Custom: &custom, Policy: policy})
}
