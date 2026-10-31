package detection

import (
	"bytes"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
)

func (r *reader) golang() {
	data, ok := r.read("go.mod")
	if !ok {
		return
	}
	manifest, err := modfile.Parse("go.mod", data, nil)
	if err != nil || manifest.Module == nil {
		r.malformed("go.mod")
		return
	}
	packages := map[string]bool{}
	var walk func(string, int)
	walk = func(directory string, depth int) {
		if r.ctx.Err() != nil {
			return
		}
		entries := r.directory(directory)
		if directory != "." {
			for _, entry := range entries {
				if entry.Name() == "go.mod" {
					r.warn("nested_module", filepath.Join(directory, "go.mod"), "A nested Go module was excluded. Inspect that module directory separately.")
					return
				}
			}
		}
		for _, entry := range entries {
			if r.ctx.Err() != nil {
				return
			}
			name := entry.Name()
			if !readableName(name) {
				r.warn("unsupported_name", directory, "An entry with an unsupported filename was ignored.")
				continue
			}
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				continue
			}
			relative := filepath.Join(directory, name)
			if entry.IsDir() {
				if slices.Contains([]string{"node_modules", "vendor", "target", "dist", "build", "coverage", "venv"}, name) {
					continue
				}
				if depth >= maxDepth {
					r.limit(relative)
					continue
				}
				walk(relative, depth+1)
				continue
			}
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			source, ok := r.read(relative)
			if !ok {
				continue
			}
			buildContext := build.Default
			buildContext.OpenFile = func(string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(source)), nil }
			matches, err := buildContext.MatchFile(directory, name)
			if err != nil {
				r.malformed(relative)
				continue
			}
			if !matches {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), relative, source, 0)
			if err != nil {
				r.malformed(relative)
				continue
			}
			if file.Name.Name != "main" {
				continue
			}
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if ok && function.Recv == nil && function.Name.Name == "main" && function.Type.TypeParams.NumFields() == 0 && function.Type.Params.NumFields() == 0 && function.Type.Results.NumFields() == 0 && function.Body != nil {
					packages[directory] = true
				}
			}
		}
	}
	walk(".", 0)
	entries := sortedKeys(packages)
	for _, directory := range entries {
		argument := "."
		if directory != "." {
			argument = "./" + filepath.ToSlash(directory)
		}
		r.candidate("Go: "+argument, "go", []string{"run", argument}, "A non-test main package with func main was found for this host's build constraints. Dependencies, compilation and runtime behavior were not checked.", "source")
	}
	if len(entries) == 0 {
		r.warn("no_go_main", "go.mod", "No runnable main package was established within the bounded source scan. This may be a library, a different build-tag configuration or a deeper/nested module.")
	}
	if len(entries) > 1 {
		r.warn("multiple_go_mains", "go.mod", "Multiple Go main packages were found. Choose the intended entry point; the repository root was not assumed to be runnable.")
	}
	r.stack("Go", nil, []string{"Go modules"}, []string{"go.mod"}, entries)
}
