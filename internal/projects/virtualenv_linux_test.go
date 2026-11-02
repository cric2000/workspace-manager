package projects

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewPreservesVirtualEnvironmentInterpreter(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python unavailable")
	}
	service, draft, _ := registryFixture(t)
	venv := filepath.Join(draft.Path, ".venv")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, python, "-m", "venv", "--without-pip", venv).CombinedOutput(); err != nil {
		t.Fatalf("test-owned venv: %s: %v", output, err)
	}
	invocation := filepath.Join(venv, "bin", "python")
	draft.Commands[0].Configuration.Executable = invocation
	// Execute only this test-owned diagnostic after approval, never user source.
	draft.Commands[0].Configuration.Arguments = []string{"-I", "-B", "-c", "import sys; print(sys.prefix)"}
	review := requireReview(t, service, draft)
	if review.Project.Commands[0].Configuration.Executable != invocation {
		t.Fatal("venv invocation was resolved to the system interpreter")
	}
	saved := requireSave(t, service, review).State.Projects[0].Project.Commands[0]
	if saved.Approval == nil || saved.Configuration.Executable != invocation {
		t.Fatal(saved)
	}
	output, err := exec.CommandContext(ctx, saved.Configuration.Executable, saved.Configuration.Arguments...).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != venv {
		t.Fatalf("venv lost: %s: %v", output, err)
	}
}

func TestReviewRejectsChangedExecutableLinkTargetAndParent(t *testing.T) {
	for _, scenario := range []string{"retarget", "recreate-link", "binary", "parent"} {
		t.Run(scenario, func(t *testing.T) {
			service, draft, _ := registryFixture(t)
			target := draft.Commands[0].Configuration.Executable
			parent := filepath.Join(t.TempDir(), "bin")
			if err := os.Mkdir(parent, 0700); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(parent, "python")
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			draft.Commands[0].Configuration.Executable = link
			review := requireReview(t, service, draft)
			switch scenario {
			case "retarget", "recreate-link":
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
				if scenario == "retarget" {
					target = filepath.Join(t.TempDir(), "other")
					if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
			case "binary":
				if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
					t.Fatal(err)
				}
			case "parent":
				if err := os.Rename(parent, parent+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(parent, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
			}
			got := service.CommitRegistration(context.Background(), review.Token)
			if got.Committed || got.Code != "review_changed" {
				t.Fatal(got)
			}
		})
	}
}
