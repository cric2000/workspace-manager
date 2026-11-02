package bootstrap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func TestFastAPIPlanMetadataAndNaming(t *testing.T) {
	registry := NewRegistry()
	plan, err := registry.Render("fastapi", Options{Name: "hello-api"})
	if err != nil {
		t.Fatal(err)
	}
	again, _ := registry.Render("fastapi", Options{Name: "hello-api"})
	if !reflect.DeepEqual(plan, again) || len(plan.Files) != 6 || len(plan.InitializationCommands) != 0 || plan.StartupProjectExecutable != ".venv/bin/python" || plan.StartupProgram != "" {
		t.Fatal(plan)
	}
	var metadata struct {
		Project struct {
			Name           string
			RequiresPython string `toml:"requires-python"`
			Dependencies   []string
			Extras         map[string][]string `toml:"optional-dependencies"`
		}
		Tool struct {
			Setuptools struct {
				Packages struct{ Find struct{ Include []string } }
			}
		}
	}
	if _, err := toml.Decode(plan.Files[0].Contents, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Project.Name != "hello-api" || metadata.Project.RequiresPython != ">=3.11" || !reflect.DeepEqual(metadata.Project.Dependencies, []string{"fastapi==0.162.0", "uvicorn==0.54.0"}) || !reflect.DeepEqual(metadata.Project.Extras["test"], []string{"httpx2==2.16.0"}) || !reflect.DeepEqual(metadata.Tool.Setuptools.Packages.Find.Include, []string{"app*"}) {
		t.Fatal(metadata)
	}
	if plan.Runtime.Validate() != nil || plan.Runtime.Readiness.URL != "http://127.0.0.1:8000/health" || plan.PreviewURL != "http://127.0.0.1:8000/docs" || plan.Runtime.Stop.Signal != "SIGTERM" {
		t.Fatal(plan)
	}
	if plan.DependencySetup.Program != "python3" || len(plan.AdditionalSetup) != 1 || plan.AdditionalSetup[0].ProjectExecutable != plan.StartupProjectExecutable || !reflect.DeepEqual(plan.AdditionalSetup[0].Arguments, []string{"-m", "pip", "install", ".[test]"}) {
		t.Fatal(plan)
	}
	for _, name := range []string{"Uppercase", "under_score", "trailing-"} {
		if _, err := registry.Render("fastapi", Options{Name: name}); err != ErrPythonName {
			t.Fatalf("accepted %q: %v", name, err)
		}
	}
	definition, err := registry.Definition("fastapi")
	if err != nil || !definition.Network || !reflect.DeepEqual(definition.RequiredTools, []string{"python3"}) {
		t.Fatal(definition, err)
	}
	plan.Runtime.Readiness.URL = "changed"
	plan.AdditionalSetup[0].Arguments[0] = "changed"
	fresh, _ := registry.Render("fastapi", Options{Name: "hello-api"})
	if !reflect.DeepEqual(fresh, again) {
		t.Fatal("plans share mutable fields")
	}
}

// Opt-in integration runs only generated fixtures. Supply an already downloaded
// wheelhouse; this test never accesses an index or installs into system Python.
func TestGeneratedFastAPIInstallsTestsAndServesOffline(t *testing.T) {
	wheels := os.Getenv("WORKSPACE_MANAGER_FASTAPI_WHEELS")
	if wheels == "" {
		t.Skip("set WORKSPACE_MANAGER_FASTAPI_WHEELS to an offline wheelhouse")
	}
	wheels, err := filepath.Abs(wheels)
	if err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := OpenParent(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	plan, _ := NewRegistry().Render("fastapi", Options{Name: "hello-api"})
	if _, err := parent.Publish(context.Background(), "hello-api", plan.Files); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(parent.Path, "hello-api")
	env := append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "PYTHONNOUSERSITE=1", "PIP_NO_INDEX=1", "PIP_DISABLE_PIP_VERSION_CHECK=1", "PIP_FIND_LINKS="+wheels, "PIP_CACHE_DIR="+filepath.Join(parent.Path, "pip-cache"))
	run := func(program string, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, program, args...)
		command.Dir = project
		command.Env = env
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s: %v", args, output, err)
		}
		return string(output)
	}
	run(python, plan.DependencySetup.Arguments...)
	local := filepath.Join(project, plan.StartupProjectExecutable)
	run(local, plan.AdditionalSetup[0].Arguments...)
	output := run(local, "-B", "-m", "unittest", "discover", "-s", "tests", "-v")
	if !strings.Contains(output, "Ran 3 tests") || !strings.Contains(output, "OK") {
		t.Fatal(output)
	}
	// A reserved ephemeral socket avoids collisions; Uvicorn accepts the inherited
	// socket while retaining the template's module, loopback and reload arguments.
	output = run(local, "-B", "-c", fastAPIServerProbe, strings.Join(plan.StartupArguments, "\x1f"))
	if !strings.Contains(output, "health, docs, schema, greeting and graceful stop verified") {
		t.Fatal(output)
	}
}

const fastAPIServerProbe = `import json, os, pathlib, signal, socket, subprocess, sys, tempfile, time, urllib.request
args = sys.argv[1].split("\x1f")
with socket.socket() as listener, tempfile.TemporaryFile() as log:
    listener.bind(("127.0.0.1", 0))
    listener.listen()
    port = listener.getsockname()[1]
    args[args.index("--port") + 1] = str(port)
    args += ["--fd", str(listener.fileno())]
    process = subprocess.Popen([sys.executable, *args], pass_fds=(listener.fileno(),), stdout=log, stderr=log, start_new_session=True)
    try:
        base = f"http://127.0.0.1:{port}"
        def get(path):
            with urllib.request.urlopen(base + path, timeout=1) as response:
                assert response.status == 200
                return response.read()
        deadline = time.monotonic() + 15
        while True:
            try:
                assert json.loads(get("/health")) == {"status": "ok"}
                break
            except (OSError, TimeoutError):
                if time.monotonic() >= deadline or process.poll() is not None:
                    log.seek(0)
                    raise AssertionError(log.read().decode())
                time.sleep(0.05)
        assert json.loads(get("/")) == {"message": "Hello from hello-api!"}
        assert b"swagger" in get("/docs").lower()
        assert "/health" in json.loads(get("/openapi.json"))["paths"]
        os.killpg(process.pid, signal.SIGTERM)
        assert process.wait(timeout=10) == 0
        print("health, docs, schema, greeting and graceful stop verified")
    finally:
        if process.poll() is None:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=5)
`
