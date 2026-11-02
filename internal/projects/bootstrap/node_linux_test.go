package bootstrap

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNodeStarterPlanAndNaming(t *testing.T) {
	registry := NewRegistry()
	plan, err := registry.Render("node-typescript", Options{Name: "hello-node"})
	if err != nil {
		t.Fatal(err)
	}
	again, _ := registry.Render("node-typescript", Options{Name: "hello-node"})
	if !reflect.DeepEqual(plan, again) {
		t.Fatal("non-deterministic plan")
	}
	if len(plan.Files) != 5 || len(plan.InitializationCommands) != 0 || plan.StartupProgram != "node" || !reflect.DeepEqual(plan.StartupArguments, []string{"--watch", "src/index.ts"}) {
		t.Fatal(plan)
	}
	if plan.DependencySetup == nil || !reflect.DeepEqual(plan.DependencySetup.Arguments, []string{"install", "--ignore-scripts"}) {
		t.Fatal(plan)
	}
	var manifest struct {
		Name            string
		Private         bool
		Type            string
		Scripts         map[string]string
		DevDependencies map[string]string
		Engines         map[string]string
	}
	if err := json.Unmarshal([]byte(plan.Files[0].Contents), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "hello-node" || !manifest.Private || manifest.Type != "module" || manifest.Scripts["dev"] != "node --watch src/index.ts" || manifest.DevDependencies["typescript"] != "7.0.2" || manifest.Engines["node"] != ">=24.12.0" {
		t.Fatal(manifest)
	}
	for _, name := range []string{"Uppercase", "under_score"} {
		if _, err := registry.Render("node-typescript", Options{Name: name}); err != ErrNodeName {
			t.Fatalf("accepted %q: %v", name, err)
		}
	}
	definition, err := registry.Definition("node-typescript")
	if err != nil || !reflect.DeepEqual(definition.RequiredTools, []string{"node", "npm"}) || len(definition.Options) != 0 {
		t.Fatal(definition, err)
	}
}

// Uses the repository's already installed, exact compiler/type packages. No
// dependency installation or network access occurs during the Go test suite.
func TestGeneratedNodeStarterRunsChecksBuildsAndWatchesOffline(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js unavailable")
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		t.Skip("npm unavailable")
	}
	_, filename, _, _ := runtime.Caller(0)
	dependencies := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../frontend/node_modules"))
	for name, expected := range map[string]string{"typescript": "7.0.2", "@types/node": "24.19.2"} {
		contents, err := os.ReadFile(filepath.Join(dependencies, name, "package.json"))
		if os.IsNotExist(err) {
			t.Skip("Install frontend dependencies to validate generated TypeScript builds")
		}
		if err != nil {
			t.Fatal(err)
		}
		var metadata struct{ Version string }
		if err := json.Unmarshal(contents, &metadata); err != nil || metadata.Version != expected {
			t.Fatal(name, metadata, err)
		}
	}
	parent, err := OpenParent(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	plan, _ := NewRegistry().Render("node-typescript", Options{Name: "hello-node"})
	if _, err := parent.Publish(context.Background(), "hello-node", plan.Files); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(parent.Path, "hello-node")
	if err := os.Symlink(dependencies, filepath.Join(project, "node_modules")); err != nil {
		t.Fatal(err)
	}
	environment := append(os.Environ(), "npm_config_cache="+filepath.Join(parent.Path, "npm-cache"), "npm_config_offline=true", "npm_config_update_notifier=false", "npm_config_audit=false", "npm_config_fund=false", "NO_COLOR=1")
	run := func(tool string, args ...string) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, tool, args...)
		command.Dir = project
		command.Env = environment
		data, err := command.CombinedOutput()
		return string(data), err
	}
	if output, err := run(node, "src/index.ts"); err != nil || strings.TrimSpace(output) != "Hello from hello-node!" {
		t.Fatal(output, err)
	}
	for _, script := range []string{"check", "build", "start"} {
		output, err := run(npm, "run", script)
		if err != nil {
			t.Fatal(script, output, err)
		}
		if script == "start" && !strings.Contains(output, "Hello from hello-node!") {
			t.Fatal(output)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	watch := exec.CommandContext(ctx, npm, "run", "dev")
	watch.Dir = project
	watch.Env = environment
	watch.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	watch.WaitDelay = time.Second
	watch.Cancel = func() error {
		if watch.Process == nil {
			return nil
		}
		return syscall.Kill(-watch.Process.Pid, syscall.SIGTERM)
	}
	output, _ := watch.CombinedOutput()
	if ctx.Err() != context.DeadlineExceeded || !strings.Contains(string(output), "Hello from hello-node!") {
		t.Fatalf("development watcher: %s / %v", output, ctx.Err())
	}
	// Native execution is not type checking: the generated check script must
	// reject a deliberately invalid assignment instead of merely running it.
	if err := os.WriteFile(filepath.Join(project, "src/index.ts"), []byte("const message: string = 123;\nconsole.log(message);\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := run(npm, "run", "check"); err == nil || !strings.Contains(output, "TS2322") {
		t.Fatal("check did not reject type error", output, err)
	}
}
