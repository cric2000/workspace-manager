package bootstrap

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"golang.org/x/mod/modfile"
)

func TestGeneratedGoApplicationBuildsAndRunsOffline(t *testing.T) {
	tool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("Go toolchain unavailable")
	}
	parent, err := OpenParent(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	plan, _ := NewRegistry().Render("go", Options{Name: "hello-go", ModulePath: "example.com/you/hello-go"})
	if _, err := parent.Publish(context.Background(), "hello-go", plan.Files); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, tool, "run", ".")
	command.Dir = filepath.Join(parent.Path, "hello-go")
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "GOENV=off", "GOPROXY=off", "GOSUMDB=off")
	output, err := command.CombinedOutput()
	if err != nil || string(output) != "Hello from hello-go!\n" {
		t.Fatalf("generated starter: %s / %v", output, err)
	}
}

func TestGoStarterIsDeterministicAndParses(t *testing.T) {
	registry := NewRegistry()
	options := Options{Name: "hello-go", ModulePath: "example.com/you/hello-go"}
	first, err := registry.Render("go", options)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := registry.Render("go", options)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("non-deterministic plan")
	}
	if len(first.Files) != 4 || len(first.InitializationCommands) != 0 || !reflect.DeepEqual(first.StartupArguments, []string{"run", "."}) {
		t.Fatal(first)
	}
	for _, file := range first.Files {
		switch file.Path {
		case "go.mod":
			module, err := modfile.Parse(file.Path, []byte(file.Contents), nil)
			if err != nil || module.Module.Mod.Path != options.ModulePath || module.Go.Version != "1.22.0" {
				t.Fatal(module, err)
			}
		case "main.go":
			if _, err := parser.ParseFile(token.NewFileSet(), file.Path, file.Contents, parser.AllErrors); err != nil {
				t.Fatal(err)
			}
		}
	}
	definitions := registry.List()
	definitions[0].RequiredTools[0] = "changed"
	definitions[0].Options[0] = "changed"
	if registry.List()[0].RequiredTools[0] != "go" || registry.List()[0].Options[0] != "modulePath" {
		t.Fatal("catalogue aliases registry")
	}
	first.Files[0].Contents = "client mutation"
	third, _ := registry.Render("go", options)
	if reflect.DeepEqual(first, third) {
		t.Fatal("plan aliases registry")
	}
}

func TestStarterRejectsUnsafeNamesModulePathsAndUnknownTemplates(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", "../escape", "/absolute", "space name", "line\nbreak", "-flag", "name\x00", "naïve"} {
		if _, err := NewRegistry().Render("go", Options{Name: name, ModulePath: "example.com/app"}); err != ErrOptions {
			t.Fatalf("accepted %q: %v", name, err)
		}
	}
	for _, module := range []string{"", "../escape", "https://example.com/app", "example.com/a\nrequire evil v1.0.0", "example.com/space name"} {
		if _, err := NewRegistry().Render("go", Options{Name: "safe", ModulePath: module}); err != ErrModulePath {
			t.Fatalf("accepted module %q: %v", module, err)
		}
	}
	if _, err := NewRegistry().Render("unknown", Options{Name: "safe"}); err != ErrOptions {
		t.Fatal(err)
	}
}
