package bootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"workspace-manager/internal/domain"
)

func TestNextPlanExplicitOptionsAndPinnedOfficialCommand(t *testing.T) {
	registry := NewRegistry()
	for _, selected := range []*NextOptions{nil, {PackageManager: "pnpm"}} {
		plan, err := registry.Render("nextjs", Options{Name: "hello-next", Next: selected})
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Files) != 0 || plan.Scaffold == nil || plan.Scaffold.Program != "npm" || plan.Runtime.Validate() != nil {
			t.Fatal(plan)
		}
		args := plan.Scaffold.Arguments
		for _, flag := range []string{"--package=create-next-app@16.4.3", "--skip-install", "--disable-git", "--no-agent-feedback", "--no-agents-md", "--no-react-compiler", "--no-cache-components", "--yes"} {
			if !slices.Contains(args, flag) {
				t.Fatal("missing explicit flag", flag, args)
			}
		}
		if selected == nil {
			for _, flag := range []string{"--ts", "--tailwind", "--app", "--src-dir", "--use-npm"} {
				if !slices.Contains(args, flag) {
					t.Fatal(args)
				}
			}
		} else {
			for _, flag := range []string{"--js", "--no-tailwind", "--no-app", "--no-src-dir", "--use-pnpm"} {
				if !slices.Contains(args, flag) {
					t.Fatal(args)
				}
			}
			if slices.Contains(plan.StartupArguments, "--") {
				t.Fatal("pnpm must forward script options directly", plan)
			}
			definition, _ := registry.Definition("nextjs")
			if !reflect.DeepEqual(SelectedTools(definition, plan), []string{"node", "npm", "pnpm"}) {
				t.Fatal(plan)
			}
		}
	}
	if _, err := registry.Render("nextjs", Options{Name: "safe", Next: &NextOptions{PackageManager: "sh"}}); err != ErrOptions {
		t.Fatal(err)
	}
	if _, err := registry.Render("nextjs", Options{Name: "BadName"}); err != ErrNodeName {
		t.Fatal(err)
	}
}

func TestOfficialNextScaffolderGeneratesSelectedOptionsOffline(t *testing.T) {
	cli := os.Getenv("WORKSPACE_MANAGER_NEXT_CLI")
	if cli == "" {
		t.Skip("set WORKSPACE_MANAGER_NEXT_CLI to the installed pinned official CLI")
	}
	cli, err := filepath.Abs(cli)
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"app-typescript-tailwind-src", "pages-javascript-root"} {
		t.Run(variant, func(t *testing.T) {
			options := Options{Name: "hello-next"}
			if strings.HasPrefix(variant, "pages") {
				options.Next = &NextOptions{PackageManager: "pnpm"}
			}
			plan, _ := NewRegistry().Render("nextjs", options)
			parent := t.TempDir()
			directory := filepath.Join(parent, "workspace-manager-next-fixture")
			action := domain.CommandAction{Executable: node, Arguments: append([]string{cli}, plan.Scaffold.Arguments[6:]...), WorkingDirectory: directory}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			files, err := PrepareScaffold(ctx, action, options.Name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatal("private workspace not cleaned", err)
			}
			generated := map[string]File{}
			for _, file := range files {
				generated[file.Path] = file
			}
			if variant == "app-typescript-tailwind-src" {
				if _, ok := generated["src/app/page.tsx"]; !ok {
					t.Fatal("missing selected output; files:", slices.Collect(func(yield func(string) bool) {
						for name := range generated {
							if !yield(name) {
								return
							}
						}
					}))
				}
				if !strings.Contains(generated["src/app/globals.css"].Contents, "tailwindcss") || !strings.Contains(generated["next.config.ts"].Contents, "@tailwindcss/turbopack") {
					t.Fatal("missing official Tailwind integration")
				}

				if _, ok := generated["tsconfig.json"]; !ok {
					t.Fatal("missing selected output; files:", slices.Collect(func(yield func(string) bool) {
						for name := range generated {
							if !yield(name) {
								return
							}
						}
					}))
				}
			} else {
				if _, ok := generated["pages/index.js"]; !ok {
					t.Fatal("missing selected output; files:", slices.Collect(func(yield func(string) bool) {
						for name := range generated {
							if !yield(name) {
								return
							}
						}
					}))
				}
				if _, ok := generated["postcss.config.mjs"]; ok {
					t.Fatal("unexpected Tailwind config")
				}
			}
			for name := range generated {
				if name == "AGENTS.md" || strings.HasPrefix(name, ".git/") || strings.HasPrefix(name, "node_modules/") || strings.Contains(name, "lock.") {
					t.Fatal("unrequested setup", name)
				}
			}
			var manifest struct{ Dependencies map[string]string }
			if json.Unmarshal([]byte(generated["package.json"].Contents), &manifest) != nil || manifest.Dependencies["next"] != NextScaffolderVersion {
				t.Fatal(generated["package.json"])
			}
			destination, err := OpenParent(parent)
			if err != nil {
				t.Fatal(err)
			}
			defer destination.Close()
			if _, err := destination.Publish(ctx, "published", files); err != nil {
				t.Fatal(err)
			}
			for _, file := range files {
				data, _ := fileBytes(file)
				published, err := os.ReadFile(filepath.Join(parent, "published", file.Path))
				if err != nil || !bytes.Equal(data, published) {
					t.Fatal("published bytes differ", file.Path, err)
				}
			}
		})
	}
}

func TestScaffolderRejectsUnsafeOutputAndCleansOnlyOwnedWorkspace(t *testing.T) {
	for _, scenario := range []string{"symlink", "fifo", "oversize", "node_modules", "failed", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			parent := t.TempDir()
			directory := filepath.Join(parent, "workspace-manager-next-fixture")
			outside := filepath.Join(t.TempDir(), "user.txt")
			if os.WriteFile(outside, []byte("keep"), 0600) != nil {
				t.Fatal("fixture")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			action := domain.CommandAction{Executable: "/bin/true", Arguments: []string{}, WorkingDirectory: directory}
			_, err := prepareScaffold(ctx, action, "fixture", func(_ context.Context, _ domain.CommandAction, _ []string) error {
				target := filepath.Join(directory, "fixture")
				if err := os.Mkdir(target, 0700); err != nil {
					return err
				}
				switch scenario {
				case "symlink":
					return os.Symlink(outside, filepath.Join(target, "link"))
				case "fifo":
					return syscall.Mkfifo(filepath.Join(target, "fifo"), 0600)
				case "oversize":
					return os.WriteFile(filepath.Join(target, "big"), bytes.Repeat([]byte("x"), (1<<20)+1), 0600)
				case "node_modules":
					return os.Mkdir(filepath.Join(target, "node_modules"), 0700)
				case "failed":
					return errors.New("raw private detail")
				case "cancelled":
					cancel()
					return ctx.Err()
				}
				return nil
			})
			if err == nil {
				t.Fatal("accepted unsafe result")
			}
			if data, _ := os.ReadFile(outside); string(data) != "keep" {
				t.Fatal("external source changed")
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatal("workspace leaked", err)
			}
		})
	}
}

func TestBinaryPublicationPreservesBytesAndRejectsInvalidEncoding(t *testing.T) {
	parent, err := OpenParent(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	data := []byte{0, 255, 1, 254, 128}
	file := File{Path: "favicon.ico", Contents: base64.StdEncoding.EncodeToString(data), Encoding: "base64"}
	if _, err := parent.Publish(context.Background(), "assets", []File{file}); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(parent.Path, "assets", file.Path))
	if err != nil || !bytes.Equal(data, actual) {
		t.Fatal(actual, err)
	}
	for _, file := range []File{{Path: "bad", Contents: "%%%", Encoding: "base64"}, {Path: "bad", Contents: "x", Encoding: "unknown"}} {
		if result, err := parent.Publish(context.Background(), "invalid", []File{file}); !errors.Is(err, ErrOptions) || result.Published {
			t.Fatal(result, err)
		}
	}
}

func TestScaffolderCancellationStopsItsOwnedProcessGroup(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("shell unavailable")
	}
	directory := t.TempDir()
	marker := filepath.Join(directory, "child")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	action := domain.CommandAction{Executable: shell, Arguments: []string{"-c", `sleep 60 & child=$!; printf '%s' "$child" > "$1"; wait`, "fixture", marker}, WorkingDirectory: directory}
	finished := make(chan error, 1)
	go func() { finished <- runScaffolder(ctx, action, os.Environ()) }()
	var child int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(marker)
		if _, err := fmt.Sscanf(string(data), "%d", &child); err == nil && child > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if child == 0 {
		cancel()
		<-finished
		t.Fatal("helper did not start")
	}
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", child))
		if os.IsNotExist(err) || bytes.Contains(status, []byte("State:\tZ")) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("owned descendant survived cancellation")
}

func TestScaffolderPinsNestedNodeInsteadOfFollowingChangedPATH(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	fake := t.TempDir()
	if err := os.WriteFile(filepath.Join(fake, "node"), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	directory := filepath.Join(t.TempDir(), "workspace-manager-next-fixture")
	child := `const fs=require('node:fs');fs.mkdirSync('fixture');fs.writeFileSync('fixture/package.json',JSON.stringify({name:'fixture',scripts:{dev:'next dev'},dependencies:{next:'16.4.3'}}));`
	encoded, _ := json.Marshal(child)
	script := `const result=require('node:child_process').spawnSync('node',['-e',` + string(encoded) + `],{stdio:'inherit'});process.exit(result.status??98);`
	action := domain.CommandAction{Executable: node, Arguments: []string{"-e", script}, WorkingDirectory: directory}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	files, err := PrepareScaffold(ctx, action, "fixture")
	if err != nil || len(files) != 1 || files[0].Path != "package.json" {
		t.Fatal("nested invocation followed unreviewed node alias", files, err)
	}
}

func TestScaffolderRetainsBoundedOutputAndRealExitStatus(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("shell unavailable")
	}
	action := domain.CommandAction{Executable: shell, Arguments: []string{"-c", "printf '%070000d' 0; exit 7"}, WorkingDirectory: t.TempDir()}
	result := runScaffolderLogged(context.Background(), action, []string{"PATH=/usr/bin:/bin", "LC_ALL=C"})
	if !result.Started || result.ExitCode != 7 || result.Err == nil || !result.Truncated || len(result.Output) != domain.MaxSetupOutputBytes {
		t.Fatal(result)
	}
}
