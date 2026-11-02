package projects

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/projects/bootstrap"
)

func TestStableVersionParsingAndMinimums(t *testing.T) {
	for _, test := range []struct{ tool, output, want string }{
		{"go", "go version go1.26.0 linux/amd64\n", "1.26.0"},
		{"node", "v24.12.0\n", "24.12.0"}, {"npm", "11.19.0", "11.19.0"},
		{"pnpm", "12.10.1\n", "12.10.1"}, {"python3", "Python 3.14.4\n", "3.14.4"},
		{"git", "git version 2.53.0", "2.53.0"}, {"pip", "pip 26.3 from /tmp/venv/lib/pip (python 3.14)", "26.3.0"},
		{"node", "v24.12.0-rc.1", ""}, {"go", "go version devel go1.27 linux/amd64", ""},
		{"python3", "Python 3.15.0a1", ""}, {"npm", "11.19.0\nprivate detail", ""},
		{"npm", "99999999999999999.0.0", ""}, {"node", "24.12.0", ""},
	} {
		if got := parseToolVersion(test.tool, test.output); got != test.want {
			t.Errorf("%s %q: got %q, want %q", test.tool, test.output, got, test.want)
		}
	}
	for _, test := range []struct {
		actual, min string
		want        bool
	}{
		{"24.12.0", "24.12.0", true}, {"24.11.99", "24.12.0", false}, {"25.0.0", "24.12.0", true},
		{"1.22.0", "1.22.0", true}, {"1.21.99", "1.22.0", false}, {"3.11.0", "3.11", true}, {"3.10.99", "3.11", false},
	} {
		if got := versionAtLeast(test.actual, test.min); got != test.want {
			t.Errorf("%s >= %s: %v", test.actual, test.min, got)
		}
	}
}

func TestVersionChecksNeedExplicitRequestAndNeverPublishOrApprove(t *testing.T) {
	service, draft := creationFixture(t)
	review := creationReview(t, service, draft)
	calls := 0
	service.versionRunner = func(_ context.Context, action domain.CommandAction, node string) bootstrap.CommandResult {
		calls++
		if node != "" || action.WorkingDirectory != "" || !reflect.DeepEqual(action.Arguments, []string{"version"}) {
			t.Fatal(action, node)
		}
		return bootstrap.CommandResult{ExitCode: 0, Output: "go version go1.22.0 linux/amd64"}
	}
	if len(review.ToolChecks) != 1 || review.ToolChecks[0].MinimumVersion != "1.22.0" || calls != 0 {
		t.Fatal(review.ToolChecks, calls)
	}
	// Returned review mutation cannot change held argv or requirements.
	review.ToolChecks[0].Arguments[0] = "run"
	review.ToolChecks[0].MinimumVersion = "99.0.0"
	got := service.CheckCreationToolVersions(context.Background(), review.Token)
	if got.Code != "" || len(got.Checks) != 1 || got.Checks[0].Status != "supported" || calls != 1 {
		t.Fatal(got, calls)
	}
	if entries, _ := os.ReadDir(draft.ParentDirectory); len(entries) != 0 {
		t.Fatal("probe published project", entries)
	}
	if outcome := service.GetCreationOutcome(context.Background(), review.Token); outcome.Code != "creation_pending" {
		t.Fatal(outcome)
	}
	created := service.CommitCreation(context.Background(), review.Token)
	if !created.Registered || created.State.Projects[0].Project.Commands[0].Approval != nil || calls != 1 {
		t.Fatal(created, calls)
	}
}

func TestVersionCheckFailuresAreBoundedAndNeverExposeRawOutput(t *testing.T) {
	for _, test := range []struct {
		name, output, status string
		err                  error
		truncated            bool
	}{
		{"old", "go version go1.21.9 linux/amd64", "unsupported", nil, false},
		{"unknown", "private credential", "unknown", nil, false},
		{"prerelease", "go version go1.27rc1 linux/amd64", "unknown", nil, false},
		{"failed", "private credential", "failed", errors.New("private credential"), false},
		{"timeout", "private credential", "timeout", context.DeadlineExceeded, false},
		{"cancelled", "private credential", "cancelled", context.Canceled, false},
		{"truncated", "go version go1.26.0 linux/amd64", "unknown", nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, draft := creationFixture(t)
			review := creationReview(t, service, draft)
			service.versionRunner = func(context.Context, domain.CommandAction, string) bootstrap.CommandResult {
				return bootstrap.CommandResult{Output: test.output, Err: test.err, Truncated: test.truncated}
			}
			result := service.CheckCreationToolVersions(context.Background(), review.Token)
			if result.Code != "version_attention" || result.Checks[0].Status != test.status || strings.Contains(result.Checks[0].Message, "private") || strings.Contains(result.Checks[0].Version, "private") {
				t.Fatal(result)
			}
		})
	}
}

func TestVersionChecksRefuseExpiredChangedAndCancelledReviews(t *testing.T) {
	for _, scenario := range []string{"expired", "changed", "cancelled", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			service, draft := creationFixture(t)
			review := creationReview(t, service, draft)
			service.versionRunner = func(context.Context, domain.CommandAction, string) bootstrap.CommandResult {
				t.Fatal("unexpected execution")
				return bootstrap.CommandResult{}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "expired":
				service.now = func() time.Time { return review.ExpiresAt.Add(time.Second) }
			case "changed":
				if err := os.WriteFile(review.ToolChecks[0].Executable, []byte("#!/bin/sh\nexit 12\n"), 0700); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			case "missing":
				review.Token = "unknown"
			}
			result := service.CheckCreationToolVersions(ctx, review.Token)
			if result.Code == "" {
				t.Fatal(result)
			}
			if scenario == "changed" && result.Checks[0].Status != "changed" {
				t.Fatal(result)
			}
		})
	}
}

func TestVersionProbeDoesNotHoldRegistryLockAndShutdownCancels(t *testing.T) {
	service, draft := creationFixture(t)
	review := creationReview(t, service, draft)
	started, done := make(chan struct{}), make(chan ToolVersionResult, 1)
	service.versionRunner = func(ctx context.Context, _ domain.CommandAction, _ string) bootstrap.CommandResult {
		close(started)
		<-ctx.Done()
		return bootstrap.CommandResult{Err: ctx.Err()}
	}
	go func() { done <- service.CheckCreationToolVersions(context.Background(), review.Token) }()
	<-started
	if result := service.ListProjects(context.Background()); result.Code != "" {
		t.Fatal(result)
	}
	if result := service.CheckCreationToolVersions(context.Background(), review.Token); result.Code != "busy" {
		t.Fatal(result)
	}
	if err := service.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.Checks[0].Status != "cancelled" {
			t.Fatal(result)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel version probe")
	}
}

func TestSetupVersionCheckUsesHeldCommandWithoutStartingSetup(t *testing.T) {
	service, creation := createdForSetup(t, "go")
	review := requireSetup(t, service, creation, "git")
	service.versionRunner = func(_ context.Context, action domain.CommandAction, node string) bootstrap.CommandResult {
		if !reflect.DeepEqual(action.Arguments, []string{"--version"}) || action.Executable != review.Action.Executable || node != "" {
			t.Fatal(action)
		}
		return bootstrap.CommandResult{Output: "git version 2.53.0"}
	}
	result := service.CheckSetupToolVersions(context.Background(), review.Token)
	if result.Code != "" || result.Checks[0].Status != "supported" || result.Checks[0].MinimumVersion != "2.28.0" {
		t.Fatal(result)
	}
	if _, err := os.Lstat(filepath.Join(creation.Path, ".git")); !os.IsNotExist(err) {
		t.Fatal("probe initialized git", err)
	}
	if outcome := service.GetSetupOutcome(context.Background(), review.Token); outcome.Code != "setup_pending" {
		t.Fatal(outcome)
	}
}

func TestInstalledStarterToolVersionsWithoutProjectExecution(t *testing.T) {
	path := os.Getenv("PATH")
	for _, template := range []string{"go", "node-typescript", "python", "nextjs"} {
		t.Run(template, func(t *testing.T) {
			definition, _ := bootstrap.NewRegistry().Definition(template)
			tools := definition.RequiredTools
			if template == "nextjs" {
				tools = append(tools, "pnpm")
			}
			for _, tool := range tools {
				if _, err := exec.LookPath(tool); err != nil {
					t.Skip(tool + " unavailable")
				}
			}
			service, _, _ := registryFixture(t)
			t.Setenv("PATH", path)
			draft := CreationDraft{TemplateID: template, Name: "probe-fixture", ParentDirectory: t.TempDir(), ModulePath: "example.com/probe"}
			if template != "go" {
				draft.ModulePath = ""
			}
			if template == "nextjs" {
				draft.Next = &bootstrap.NextOptions{TypeScript: true, AppRouter: true, PackageManager: "pnpm"}
			}
			review := creationReview(t, service, draft)
			result := service.CheckCreationToolVersions(context.Background(), review.Token)
			if result.Code != "" {
				t.Fatal(result)
			}
			for _, check := range result.Checks {
				if check.Version == "" || (check.Status != "supported" && check.Status != "detected") {
					t.Fatal(check)
				}
			}
			if entries, _ := os.ReadDir(draft.ParentDirectory); len(entries) != 0 {
				t.Fatal("probe created project", entries)
			}
		})
	}
}

func TestVersionCheckDiscardsResultWhenExecutableChangesDuringProbe(t *testing.T) {
	service, draft := creationFixture(t)
	review := creationReview(t, service, draft)
	service.versionRunner = func(_ context.Context, action domain.CommandAction, _ string) bootstrap.CommandResult {
		if err := os.WriteFile(action.Executable, []byte("#!/bin/sh\nexit 13\n"), 0700); err != nil {
			t.Fatal(err)
		}
		return bootstrap.CommandResult{Output: "go version go1.26.0 linux/amd64"}
	}
	result := service.CheckCreationToolVersions(context.Background(), review.Token)
	if result.Code != "version_attention" || result.Checks[0].Status != "changed" || result.Checks[0].Version != "" {
		t.Fatal(result)
	}
}

func TestSetupVersionCheckRejectsChangedSourceWithoutExecutingTools(t *testing.T) {
	service, creation := createdForSetup(t, "node-typescript")
	review := requireSetup(t, service, creation, "dependencies")
	service.versionRunner = func(context.Context, domain.CommandAction, string) bootstrap.CommandResult {
		t.Fatal("probe executed after source changed")
		return bootstrap.CommandResult{}
	}
	if err := os.WriteFile(filepath.Join(creation.Path, ".npmrc"), []byte("unreviewed"), 0600); err != nil {
		t.Fatal(err)
	}
	if result := service.CheckSetupToolVersions(context.Background(), review.Token); result.Code != "review_changed" {
		t.Fatal(result)
	}
}
