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

func TestPythonStarterPlanMetadataAndNaming(t *testing.T) {
	registry := NewRegistry()
	plan, err := registry.Render("python", Options{Name: "hello-python"})
	if err != nil {
		t.Fatal(err)
	}
	again, _ := registry.Render("python", Options{Name: "hello-python"})
	if !reflect.DeepEqual(plan, again) {
		t.Fatal("non-deterministic plan")
	}
	if len(plan.Files) != 5 || len(plan.InitializationCommands) != 0 || plan.StartupProgram != "python3" || !reflect.DeepEqual(plan.StartupArguments, []string{"-B", "main.py"}) {
		t.Fatal(plan)
	}
	if plan.DependencySetup == nil || plan.DependencySetup.Label != "Optional virtual environment" || !reflect.DeepEqual(plan.DependencySetup.Arguments, []string{"-m", "venv", ".venv"}) {
		t.Fatal(plan)
	}
	var metadata struct {
		Project struct {
			Name           string
			Version        string
			RequiresPython string `toml:"requires-python"`
			Dependencies   []string
			Scripts        map[string]string
		}
		Build struct {
			Requires []string
			Backend  string `toml:"build-backend"`
		} `toml:"build-system"`
		Tool struct {
			Setuptools struct {
				Modules []string `toml:"py-modules"`
			}
		}
	}
	if _, err := toml.Decode(plan.Files[0].Contents, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Project.Name != "hello-python" || metadata.Project.RequiresPython != ">=3.11" || len(metadata.Project.Dependencies) != 0 || metadata.Project.Scripts["hello-python"] != "main:main" || metadata.Build.Backend != "setuptools.build_meta" || !reflect.DeepEqual(metadata.Tool.Setuptools.Modules, []string{"main"}) {
		t.Fatal(metadata)
	}
	for _, name := range []string{"Uppercase", "under_score", "trailing-"} {
		if _, err := registry.Render("python", Options{Name: name}); err != ErrPythonName {
			t.Fatalf("accepted %q: %v", name, err)
		}
	}
	for _, name := range []string{"a", "1", "a-b", "1-app"} {
		if _, err := registry.Render("python", Options{Name: name}); err != nil {
			t.Fatalf("rejected %q: %v", name, err)
		}
	}
	definition, err := registry.Definition("python")
	if err != nil || !reflect.DeepEqual(definition.RequiredTools, []string{"python3"}) || len(definition.Options) != 0 {
		t.Fatal(definition, err)
	}
}

// All commands below run only our generated fixtures, never an imported project.
// Runtime/tests and virtual environment creation require no network access.
func TestGeneratedPythonStarterRunsTestsAndPackagesOffline(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python unavailable")
	}
	parent, err := OpenParent(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	plan, _ := NewRegistry().Render("python", Options{Name: "hello-python"})
	if _, err := parent.Publish(context.Background(), "hello-python", plan.Files); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(parent.Path, "hello-python")
	environment := append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "PYTHONNOUSERSITE=1", "PIP_NO_INDEX=1", "PIP_DISABLE_PIP_VERSION_CHECK=1", "PIP_CACHE_DIR="+filepath.Join(parent.Path, "pip-cache"))
	run := func(tool, directory string, args ...string) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, tool, args...)
		command.Dir = directory
		command.Env = environment
		output, err := command.CombinedOutput()
		return string(output), err
	}
	if output, err := run(python, project, "-B", "main.py"); err != nil || output != "Hello from hello-python!\n" {
		t.Fatal(output, err)
	}
	if output, err := run(python, project, "-B", "-m", "unittest", "discover", "-s", "tests", "-v"); err != nil || !strings.Contains(output, "test_greeting") || !strings.Contains(output, "OK") {
		t.Fatal(output, err)
	}
	if output, err := run(python, project, "-I", "-B", "-c", "import pathlib, tomllib; p=tomllib.loads(pathlib.Path('pyproject.toml').read_text()); assert p['project']['scripts']['hello-python']=='main:main'"); err != nil {
		t.Fatal(output, err)
	}
	if _, err := os.Stat(filepath.Join(project, "__pycache__")); !os.IsNotExist(err) {
		t.Fatal("runtime created bytecode cache", err)
	}
	if _, err := os.Stat(filepath.Join(project, "tests/__pycache__")); !os.IsNotExist(err) {
		t.Fatal("tests created bytecode cache", err)
	}
	t.Run("optional virtual environment", func(t *testing.T) {
		if output, err := run(python, project, "-I", "-B", "-c", "import ensurepip"); err != nil {
			t.Skip("ensurepip unavailable; install python3-venv to validate optional environment setup: " + output)
		}
		if output, err := run(python, project, plan.DependencySetup.Arguments...); err != nil {
			t.Fatal(output, err)
		}
		interpreter := filepath.Join(project, ".venv/bin/python")
		if output, err := run(interpreter, project, "-B", "-c", "import sys; assert sys.prefix != sys.base_prefix; assert sys.prefix.endswith('/.venv')"); err != nil {
			t.Fatal(output, err)
		}
		if output, err := run(interpreter, project, "-B", "main.py"); err != nil || output != "Hello from hello-python!\n" {
			t.Fatal(output, err)
		}
		if output, err := run(interpreter, project, "-B", "-m", "unittest", "discover", "-s", "tests", "-v"); err != nil || !strings.Contains(output, "OK") {
			t.Fatal(output, err)
		}
	})
	t.Run("wheel and console entry point", func(t *testing.T) {
		if output, err := run(python, project, "-I", "-B", "-c", "import setuptools.build_meta"); err != nil {
			t.Skip("setuptools unavailable for offline wheel validation: " + output)
		}
		wheelDirectory := filepath.Join(parent.Path, "wheels")
		if err := os.Mkdir(wheelDirectory, 0700); err != nil {
			t.Fatal(err)
		}
		if output, err := run(python, project, "-I", "-B", "-c", "from setuptools.build_meta import build_wheel; import sys; build_wheel(sys.argv[1])", wheelDirectory); err != nil {
			t.Fatal(output, err)
		}
		unpacked := filepath.Join(parent.Path, "unpacked-wheel")
		script := "import pathlib, sys, zipfile; from importlib.metadata import distributions; wheel=next(pathlib.Path(sys.argv[1]).glob('*.whl')); zipfile.ZipFile(wheel).extractall(sys.argv[2]); sys.path.insert(0,sys.argv[2]); d=next(distributions(path=[sys.argv[2]])); assert d.metadata['Name']=='hello-python'; ep=next(e for e in d.entry_points if e.group=='console_scripts' and e.name=='hello-python'); ep.load()()"
		if output, err := run(python, parent.Path, "-I", "-B", "-c", script, wheelDirectory, unpacked); err != nil || output != "Hello from hello-python!\n" {
			t.Fatal(output, err)
		}
	})
}
