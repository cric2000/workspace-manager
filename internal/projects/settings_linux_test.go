package projects

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"workspace-manager/internal/domain"
)

func configureSettings(draft *RegistrationDraft) {
	tool := draft.Commands[0].Configuration.Executable
	draft.Preferences = &PreferencesDraft{PreviewURL: "http://localhost:8123/docs", Editor: &domain.CommandAction{Executable: tool, Arguments: []string{draft.Path, ""}, WorkingDirectory: draft.Path}}
	draft.Commands[0].Configuration.Runtime = &domain.CommandRuntime{EnvironmentFiles: []string{".env", "config/later.env"}, DelaySeconds: 3, Readiness: &domain.ReadinessCheck{Kind: "tcp", Host: "::1", Port: 8123, TimeoutSeconds: 30, IntervalSeconds: 1}, Stop: &domain.StopPolicy{Signal: "SIGINT", GraceSeconds: 12, Action: &domain.CommandAction{Executable: tool, Arguments: []string{"stop", "literal ; $HOME", ""}, WorkingDirectory: draft.Path}}}
}

func TestSettingsReviewedSavedAndReopenedWithoutReadingOrExecuting(t *testing.T) {
	service, draft, paths := registryFixture(t)
	configureSettings(&draft)
	secret := []byte("PRIVATE=never-copy-environment-contents\n")
	file := filepath.Join(draft.Path, ".env")
	if err := os.WriteFile(file, secret, 0o000); err != nil {
		t.Fatal(err)
	}
	review := requireReview(t, service, draft)
	if review.Project.Preferences.Approval != nil {
		t.Fatal("premature preference approval")
	}
	payload, _ := json.Marshal(review)
	if bytes.Contains(payload, []byte("never-copy")) {
		t.Fatal("environment secret exposed")
	}
	// The held plan must not alias returned settings or the caller's draft.
	review.Project.Commands[0].Configuration.Runtime.EnvironmentFiles[0] = "tampered.env"
	review.Project.Commands[0].Configuration.Runtime.Stop.Action.Arguments[0] = "tampered"
	review.Project.Preferences.Editor.Arguments[0] = "tampered"
	draft.Commands[0].Configuration.Runtime.Readiness.Port = 9000
	result := requireSave(t, service, review)
	project := result.State.Projects[0].Project
	if project.Validate() != nil || project.Preferences.Approval == nil || project.Commands[0].Approval == nil || project.Commands[0].Configuration.Runtime.EnvironmentFiles[0] != ".env" || project.Commands[0].Configuration.Runtime.Readiness.Port != 8123 || project.Preferences.Editor.Arguments[0] != draft.Path {
		t.Fatal(project)
	}
	stored, err := os.ReadFile(filepath.Join(paths.ProjectsDir(), string(project.ID)+".json"))
	if err != nil || bytes.Contains(stored, []byte("never-copy")) {
		t.Fatal("secret saved", err)
	}
	if _, err := os.Stat(filepath.Join(draft.Path, "must-not-run")); !os.IsNotExist(err) {
		t.Fatal("editor, stop or startup action ran")
	}
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := os.ReadFile(file)
	if !bytes.Equal(secret, unchanged) {
		t.Fatal("environment file changed")
	}
	if err := service.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	reopened := NewService(nil)
	reopened.paths = service.paths
	defer reopened.ServiceShutdown()
	loaded := reopened.ListProjects(context.Background())
	if loaded.Code != "" || !reflect.DeepEqual(project, loaded.State.Projects[0].Project) {
		t.Fatal(loaded)
	}
}

func TestEnvironmentReferencesRejectSymlinksAndSpecialFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "parent symlink", "fifo", "directory", "escape"} {
		t.Run(kind, func(t *testing.T) {
			service, draft, _ := registryFixture(t)
			configureSettings(&draft)
			path := filepath.Join(draft.Path, ".env")
			switch kind {
			case "symlink":
				if err := os.Symlink(filepath.Join(filepath.Dir(draft.Path), "outside.env"), path); err != nil {
					t.Fatal(err)
				}
			case "parent symlink":
				if err := os.Symlink(filepath.Dir(draft.Path), filepath.Join(draft.Path, "config")); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "escape":
				draft.Commands[0].Configuration.Runtime.EnvironmentFiles = []string{"../outside.env"}
			}
			result := service.ReviewRegistration(context.Background(), draft)
			want := "environment_reference"
			if kind == "escape" {
				want = "settings"
			}
			if result.Code != want || result.Review != nil {
				t.Fatal(kind, result)
			}
		})
	}
	service, draft, _ := registryFixture(t)
	configureSettings(&draft)
	review := requireReview(t, service, draft)
	if err := os.Symlink("/etc/passwd", filepath.Join(draft.Path, ".env")); err != nil {
		t.Fatal(err)
	}
	if result := service.CommitRegistration(context.Background(), review.Token); result.Code != "review_changed" || result.Committed {
		t.Fatal("changed reference committed", result)
	}
}

func TestEditorAndStopActionsRequireValidPathsAndUnchangedExecutables(t *testing.T) {
	for _, kind := range []string{"editor", "stop"} {
		t.Run(kind, func(t *testing.T) {
			service, draft, _ := registryFixture(t)
			configureSettings(&draft)
			tool := filepath.Join(filepath.Dir(draft.Path), "extra-tool")
			if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			if kind == "editor" {
				draft.Preferences.Editor.Executable = tool
			} else {
				draft.Commands[0].Configuration.Runtime.Stop.Action.Executable = tool
			}
			review := requireReview(t, service, draft)
			if err := os.WriteFile(tool, []byte("#!/bin/sh\n# modified\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			if got := service.CommitRegistration(context.Background(), review.Token); got.Code != "review_changed" || got.Committed {
				t.Fatal(got)
			}
		})
	}
	service, draft, _ := registryFixture(t)
	configureSettings(&draft)
	draft.Preferences.Editor.WorkingDirectory = filepath.Dir(draft.Path)
	if got := service.ReviewRegistration(context.Background(), draft); got.Code != "directory" {
		t.Fatal(got)
	}
	draft.Preferences.Editor.WorkingDirectory = draft.Path
	draft.Commands[0].Configuration.Runtime.Readiness = &domain.ReadinessCheck{Kind: "http", URL: "https://remote.example.test/health", TimeoutSeconds: 30, IntervalSeconds: 1, ExpectedStatus: 200}
	if got := service.ReviewRegistration(context.Background(), draft); got.Code != "settings" {
		t.Fatal(got)
	}
}

func TestOversizedSettingsStayUnsignedAndUnwritten(t *testing.T) {
	service, draft, paths := registryFixture(t)
	configureSettings(&draft)
	command := draft.Commands[0]
	argument := strings.Repeat("\x01", 8192) // Valid argv; JSON expands each byte to six.
	command.Configuration.Arguments = []string{argument, argument, argument, argument}
	command.Configuration.Runtime.Stop.Action.Arguments = []string{argument, argument, argument, argument}
	draft.Commands = make([]CommandDraft, 16)
	for i := range draft.Commands {
		draft.Commands[i] = command
	}
	result := service.ReviewRegistration(context.Background(), draft)
	if result.Code != "settings" || result.Review != nil || len(service.reviews) != 0 {
		t.Fatal(result)
	}
	files, _ := filepath.Glob(filepath.Join(paths.ProjectsDir(), "*.json"))
	if len(files) != 0 {
		t.Fatal("oversized draft wrote a record")
	}
}
