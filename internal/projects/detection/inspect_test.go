package detection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, data := range files {
		put(t, root, name, data)
	}
	return root
}
func put(t *testing.T, root, name, data string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
func inspect(t *testing.T, root string) Report {
	t.Helper()
	report, err := (LocalInspector{}).Inspect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return report
}
func finding(report Report, code string) bool {
	return slices.ContainsFunc(report.Findings, func(f Finding) bool { return f.Code == code })
}
func commands(report Report) []string {
	result := []string{}
	for _, c := range report.Candidates {
		result = append(result, c.Program+" "+strings.Join(c.Arguments, " "))
	}
	return result
}

func TestJavascriptUsesDeclaredScriptsAndManagerEvidence(t *testing.T) {
	for _, pair := range [][2]string{{"package-lock.json", "npm"}, {"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"}} {
		t.Run(pair[0], func(t *testing.T) {
			root := fixture(t, map[string]string{"package.json": `{"scripts":{"dev":"secret-inline-value","build":"compile"},"dependencies":{"next":"1","react":"1"},"devDependencies":{"typescript":"1"}}`, pair[0]: "\x00binary lock content"})
			report := inspect(t, root)
			if !reflect.DeepEqual(commands(report), []string{pair[1] + " run dev"}) || report.Stacks[0].Language != "TypeScript" || !slices.Contains(report.Stacks[0].Frameworks, "Next.js") {
				t.Fatal(report)
			}
			encoded, _ := json.Marshal(report)
			if strings.Contains(string(encoded), "secret-inline-value") {
				t.Fatal("script bodies escaped into inspection results")
			}
			if report.Partial {
				t.Fatal(report.Findings)
			}
		})
	}
}

func TestJavascriptAmbiguityAndMalformedMetadata(t *testing.T) {
	root := fixture(t, map[string]string{"package.json": `{"packageManager":"pnpm@9.0.0","workspaces":["apps/*"],"scripts":{"dev":"server","--bad":"unknown"}}`, "package-lock.json": "{}"})
	report := inspect(t, root)
	if !finding(report, "manager_conflict") || !finding(report, "workspace_packages") || !finding(report, "unsupported_script") || len(report.Candidates) != 2 {
		t.Fatal(report)
	}
	for _, manifest := range []string{`{"scripts":{"dev":"a","dev":"b"}}`, `{"scripts":7}`, `{"dependencies":{"next":42}}`, `{"scripts":{"dev":"x"}} trailing`, `[]`, "{bad", strings.Repeat("[", 100) + strings.Repeat("]", 100)} {
		put(t, root, "package.json", manifest)
		report := inspect(t, root)
		if !finding(report, "malformed") || len(report.Candidates) != 0 {
			t.Fatal("malformed metadata produced a candidate", report)
		}
	}
	put(t, root, "package-lock.json", "{}")
	if err := os.Remove(filepath.Join(root, "package-lock.json")); err != nil {
		t.Fatal(err)
	}
	put(t, root, "package.json", `{"scripts":{"dev":"x"}}`)
	if report := inspect(t, root); !finding(report, "manager_unknown") || len(report.Candidates) != 0 {
		t.Fatal("manager silently selected", report)
	}
}

func TestGoFindsMultipleMainsAndExcludesTestsTagsAndNestedModules(t *testing.T) {
	root := fixture(t, map[string]string{
		"go.mod": "module example.test/project\n\ngo 1.26\n", "lib.go": "package library\n",
		"cmd/api/main.go": "package main\nfunc main() {}\n", "cmd/worker/main.go": "package main\nfunc main() {}\n",
		"main_test.go": "package main\nfunc main() {}\n", "tools/main.go": "//go:build workspace_inspector_excluded\n\npackage main\nfunc main() {}\n",
		"nested/go.mod": "module example.test/nested\n", "nested/main.go": "package main\nfunc main() {}\n",
		"vendor/ignore/main.go": "package main\nfunc main() {}\n", "node_modules/ignore/main.go": "package main\nfunc main() {}\n",
	})
	report := inspect(t, root)
	if !reflect.DeepEqual(commands(report), []string{"go run ./cmd/api", "go run ./cmd/worker"}) || !finding(report, "multiple_go_mains") || !finding(report, "nested_module") {
		t.Fatal(report)
	}
	put(t, root, "go.mod", "not valid go module syntax")
	if report := inspect(t, root); !finding(report, "malformed") || len(report.Candidates) != 0 {
		t.Fatal(report)
	}
}

func TestPythonUsesExplicitEntrypointsWithoutImportingSource(t *testing.T) {
	root := fixture(t, map[string]string{
		"pyproject.toml":   "[project]\nname='fixture'\ndependencies=['fastapi>=0.100']\n[project.scripts]\nserve='app.cli:main'\n[tool.fastapi]\nentrypoint='app.main:app'\n",
		"requirements.txt": "Django>=5\n-r private-requirements.txt\n", "manage.py": "raise RuntimeError('must never run')", "app/main.py": "raise RuntimeError('must never import')",
	})
	report := inspect(t, root)
	if !reflect.DeepEqual(commands(report), []string{"python3 manage.py runserver", "fastapi dev --entrypoint app.main:app", "serve "}) || !finding(report, "requirements_indirect") {
		t.Fatal(report)
	}
	if !slices.Contains(report.Stacks[0].Frameworks, "FastAPI") || !slices.Contains(report.Stacks[0].Frameworks, "Django") {
		t.Fatal(report)
	}
	put(t, root, "pyproject.toml", "[project]\ndependencies=['fastapi']\n")
	if report := inspect(t, root); !finding(report, "python_entrypoint") || slices.ContainsFunc(report.Candidates, func(c Candidate) bool { return c.Program == "fastapi" }) {
		t.Fatal("ASGI target guessed", report)
	}
}

func TestCargoAutoDiscoveryExplicitFeaturesAndWorkspaceRoot(t *testing.T) {
	root := fixture(t, map[string]string{"Cargo.toml": "[package]\nname='sample'\nversion='0.1.0'\nedition='2024'\n[[bin]]\nname='admin'\nrequired-features=['admin']\n", "src/main.rs": "fn main() {}", "src/bin/worker.rs": "fn main() {}", "src/bin/admin.rs": "fn main() {}"})
	report := inspect(t, root)
	if !reflect.DeepEqual(commands(report), []string{"cargo run --bin sample", "cargo run --bin worker"}) || !finding(report, "cargo_features") || !finding(report, "multiple_cargo_bins") {
		t.Fatal(report)
	}
	put(t, root, "Cargo.toml", "[package]\nname='sample'\nversion='0.1.0'\nautobins=false\n")
	if report := inspect(t, root); len(report.Candidates) != 0 || !finding(report, "no_cargo_bin") {
		t.Fatal(report)
	}
	put(t, root, "Cargo.toml", "[workspace]\nmembers=['member']\n")
	if report := inspect(t, root); len(report.Candidates) != 0 || !finding(report, "cargo_workspace") {
		t.Fatal(report)
	}
}

func TestComposeAndMakeRemainStaticAndExposeAlternatives(t *testing.T) {
	root := fixture(t, map[string]string{"compose.yaml": "services:\n  api:\n    image: example\n", "docker-compose.yml": "services:\n  worker:\n    build: .\n", "Makefile": "TOKEN := $(shell touch sentinel)\ninclude private.mk\n# serve: is only a comment\nrun ::= not-a-target\n.PHONY: dev\ndev start:\n\t@echo never run\nbuild:\n\t@echo no\n"})
	report := inspect(t, root)
	if !finding(report, "multiple_compose") || !finding(report, "make_static_only") || !finding(report, "mixed_stack") || len(report.Candidates) != 4 {
		t.Fatal(report)
	}
	if _, err := os.Stat(filepath.Join(root, "sentinel")); !os.IsNotExist(err) {
		t.Fatal("Makefile evaluated")
	}
	put(t, root, "compose.yaml", "services:\n  api: {}\n  api: {}\n")
	if report := inspect(t, root); !finding(report, "malformed") {
		t.Fatal("duplicate Compose service accepted", report)
	}
}

func TestSourceBudgetsAndIntermediateSymlinks(t *testing.T) {
	t.Run("file count", func(t *testing.T) {
		root := fixture(t, map[string]string{"go.mod": "module example.test/budget\n"})
		for index := range maxFiles + 1 {
			put(t, root, fmt.Sprintf("file%03d.go", index), "package library\n")
		}
		if got := inspect(t, root); !got.Partial || !finding(got, "scan_limit") {
			t.Fatal("source count not bounded", got)
		}
	})
	t.Run("total bytes", func(t *testing.T) {
		root := fixture(t, map[string]string{"go.mod": "module example.test/budget\n"})
		source := "package library\n//" + strings.Repeat("x", maxFileBytes-32)
		for index := range 17 {
			put(t, root, fmt.Sprintf("file%03d.go", index), source)
		}
		if got := inspect(t, root); !got.Partial || !finding(got, "scan_limit") {
			t.Fatal("byte budget not enforced", got)
		}
	})
	t.Run("intermediate symlink", func(t *testing.T) {
		outside := fixture(t, map[string]string{"main.rs": "fn main() {}"})
		root := fixture(t, map[string]string{"Cargo.toml": "[package]\nname='test'\nversion='0.1.0'\nedition='2024'\n"})
		if err := os.Symlink(outside, filepath.Join(root, "src")); err != nil {
			t.Fatal(err)
		}
		if got := inspect(t, root); len(got.Candidates) != 0 || !finding(got, "unreadable") {
			t.Fatal("intermediate link followed", got)
		}
	})
}

func TestScannerRefusesSymlinksSpecialFilesAndOversizedMetadata(t *testing.T) {
	outside := fixture(t, map[string]string{"package.json": `{"scripts":{"dev":"secret"},"packageManager":"npm@1"}`})
	root := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "package.json"), filepath.Join(root, "package.json")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "requirements.txt"), 0o600); err != nil {
		t.Fatal(err)
	}
	put(t, root, "Cargo.toml", strings.Repeat("x", maxFileBytes+1))
	put(t, root, ".env", "private-value")
	report := inspect(t, root)
	if !report.Partial || !finding(report, "unreadable") || !finding(report, "scan_limit") || len(report.Candidates) != 0 {
		t.Fatal(report)
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), "private-value") || strings.Contains(string(encoded), outside) {
		t.Fatal("unrequested content exposed")
	}
	link := filepath.Join(t.TempDir(), "selected-project")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if canonical := inspect(t, link).Path; canonical != root {
		t.Fatal("selected root not canonical", canonical)
	}
}

func TestBudgetsEmptyDirectoryAndCancellation(t *testing.T) {
	root := t.TempDir()
	if report := inspect(t, root); !finding(report, "no_metadata") || len(report.Candidates) != 0 {
		t.Fatal(report)
	}
	put(t, root, "go.mod", "module example.test/bounded\n")
	put(t, root, "a/b/c/d/e/main.go", "package main\nfunc main() {}\n")
	if report := inspect(t, root); !report.Partial || !finding(report, "scan_limit") || len(report.Candidates) != 0 {
		t.Fatal("unbounded traversal", report)
	}
	put(t, root, "pyproject.toml", "a="+strings.Repeat("[", 200)+strings.Repeat("]", 200))
	if report := inspect(t, root); !finding(report, "scan_limit") {
		t.Fatal("unbounded recursive parse", report)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (LocalInspector{}).Inspect(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, path := range []string{"relative", filepath.Join(root, "missing"), filepath.Join(root, "go.mod")} {
		if _, err := (LocalInspector{}).Inspect(context.Background(), path); !errors.Is(err, ErrDirectory) {
			t.Fatal(path, err)
		}
	}
}

func TestInspectionDoesNotInvokeToolsAndReportsDeterministically(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "must-not-run")
	root := fixture(t, map[string]string{"package.json": `{"packageManager":"npm@1","scripts":{"dev":"touch must-not-exist"}}`, "bin/npm": "#!/bin/sh\ntouch \"" + sentinel + "\"\n"})
	if err := os.Chmod(filepath.Join(root, "bin/npm"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin"))
	first, second := inspect(t, root), inspect(t, root)
	if !reflect.DeepEqual(first, second) || !first.Tools[0].Available {
		t.Fatal("unstable detection/tool presence", first, second)
	}
	if _, err := os.Stat(filepath.Join(root, "must-not-exist")); !os.IsNotExist(err) {
		t.Fatal("project script ran")
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("tool was invoked during detection")
	}
	// Source bytes remain identical, even though candidates refer to execution.
	data, _ := os.ReadFile(filepath.Join(root, "package.json"))
	if !strings.Contains(string(data), "touch must-not-exist") {
		t.Fatal("metadata was rewritten")
	}
}

func TestEntryBudgetLimitsDirectoryEnumeration(t *testing.T) {
	root := fixture(t, map[string]string{"go.mod": "module example.test/bounded\n"})
	for index := range maxEntries + 1 {
		put(t, root, fmt.Sprintf("f%04d.txt", index), "x")
	}
	if report := inspect(t, root); !report.Partial || !finding(report, "scan_limit") {
		t.Fatal("entry budget not enforced")
	}
}

func TestInvalidEncodingAndMalformedStructuredFilesStayDiagnosticOnly(t *testing.T) {
	for _, data := range []string{"\xffprivate-content", "{\"name\":\"nul\x00private-content\"}"} {
		root := fixture(t, map[string]string{"package.json": data})
		got := inspect(t, root)
		if !got.Partial || !finding(got, "invalid_text") || len(got.Candidates) != 0 {
			t.Fatal(got)
		}
		encoded, _ := json.Marshal(got)
		if strings.Contains(string(encoded), "private-content") {
			t.Fatal("invalid source content disclosed")
		}
	}
	for _, test := range []struct{ file, data string }{
		{"pyproject.toml", "[project]\nname='one'\nname='duplicate'\n"},
		{"Cargo.toml", "[package]\nname=123\n"},
		{"compose.yaml", "services: {api: {}}\n---\nservices: {second: {}}\n"},
	} {
		got := inspect(t, fixture(t, map[string]string{test.file: test.data}))
		if !got.Partial || !finding(got, "malformed") || len(got.Candidates) != 0 {
			t.Fatal(got)
		}
	}
}

func TestSelectedPathWithSpacesAndSymlinkedGoSource(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project with spaces")
	put(t, root, "go.mod", "module example.test/spaces\n")
	outside := fixture(t, map[string]string{"main.go": "package main\nfunc main() {}\n"})
	if err := os.Symlink(filepath.Join(outside, "main.go"), filepath.Join(root, "main.go")); err != nil {
		t.Fatal(err)
	}
	got := inspect(t, root)
	if got.Path != root || len(got.Candidates) != 0 || !finding(got, "unreadable") {
		t.Fatal(got)
	}
	if err := os.Remove(filepath.Join(root, "main.go")); err != nil {
		t.Fatal(err)
	}
	put(t, root, "main.go", "package main\nfunc main() {}\n")
	got = inspect(t, root)
	if len(got.Candidates) != 1 || got.Candidates[0].WorkingDirectory != root {
		t.Fatal(got)
	}
}

func TestSelectedRootCannotBlockOnFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := (LocalInspector{}).Inspect(context.Background(), path); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrDirectory) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("opening selected root blocked on a FIFO")
	}
}
