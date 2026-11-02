// Package bootstrap defines deterministic starters and Linux staged publication.
// Rendering a template never reads a project, writes files or executes tools.
package bootstrap

import (
	"errors"
	"regexp"
	"slices"
	"strconv"

	"golang.org/x/mod/module"

	"workspace-manager/internal/domain"
)

type Definition struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Options        []string `json:"options"`
	RequiredTools  []string `json:"requiredTools"`
	MinimumVersion string   `json:"minimumVersion"`
	Network        bool     `json:"network"`
	Requirements   string   `json:"requirements,omitempty"`
	NameHint       string   `json:"nameHint,omitempty"`
}

type Options struct {
	Name       string       `json:"name"`
	ModulePath string       `json:"modulePath"`
	Next       *NextOptions `json:"next,omitempty"`
}

type File struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
	Encoding string `json:"encoding,omitempty"`
}

type Plan struct {
	TemplateID               string                 `json:"templateId"`
	Files                    []File                 `json:"files"`
	InitializationCommands   []string               `json:"initializationCommands"`
	StartupProgram           string                 `json:"startupProgram"`
	StartupArguments         []string               `json:"startupArguments"`
	Preview                  string                 `json:"preview"`
	StartupName              string                 `json:"startupName,omitempty"`
	DependencySetup          *Setup                 `json:"dependencySetup,omitempty"`
	AdditionalSetup          []Setup                `json:"additionalSetup,omitempty"`
	StartupProjectExecutable string                 `json:"startupProjectExecutable,omitempty"`
	Runtime                  *domain.CommandRuntime `json:"runtime,omitempty"`
	PreviewURL               string                 `json:"previewUrl,omitempty"`
	Scaffold                 *Setup                 `json:"scaffold,omitempty"`
}

// Setup describes a separate, unexecuted dependency step. Programs and argv
// remain distinct so a later approved installer need not interpret shell text.
type Setup struct {
	Program           string   `json:"program"`
	ProjectExecutable string   `json:"projectExecutable,omitempty"`
	Arguments         []string `json:"arguments"`
	Note              string   `json:"note"`
	Label             string   `json:"label,omitempty"`
}

type starter struct {
	definition Definition
	render     func(Options) (Plan, error)
}

type Registry struct{ starters []starter }

var ErrOptions = errors.New("invalid starter options")
var ErrModulePath = errors.New("invalid Go module path")
var ErrNodeName = errors.New("invalid npm package name")
var ErrPythonName = errors.New("invalid Python distribution name")
var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func NewRegistry() Registry {
	return Registry{starters: []starter{{definition: Definition{
		ID: "go", Name: "Go application", Description: "A small command-line application using only the standard library.",
		Options: []string{"modulePath"}, RequiredTools: []string{"go"}, MinimumVersion: "1.22.0",
		Requirements: "Go 1.22.0 or later.", NameHint: "1–64 letters, digits, hyphens or underscores; start with a letter or digit.",
	}, render: renderGo}, {definition: Definition{
		ID: "node-typescript", Name: "Node.js + TypeScript", Description: "A command-line TypeScript application with native Node development and a compiled JavaScript build.",
		Options: []string{}, RequiredTools: []string{"node", "npm"}, MinimumVersion: "24.12.0",
		Requirements: "Node.js 24.12.0 or later and npm.", NameHint: "1–64 lowercase letters, digits or hyphens; start with a letter or digit.",
	}, render: renderNode}, {definition: Definition{
		ID: "python", Name: "Python application", Description: "A standard-library command-line application with unittest coverage and Python packaging metadata.",
		Options: []string{}, RequiredTools: []string{"python3"}, MinimumVersion: "3.11",
		Requirements: "Python 3.11 or later. Optional virtual environments may require the python3-venv system package.",
		NameHint:     "1–64 lowercase letters, digits or hyphens; start and end with a letter or digit.",
	}, render: renderPython}, {definition: Definition{
		ID: "fastapi", Name: "FastAPI application", Description: "A localhost API with a health endpoint, interactive docs and isolated Python dependencies.",
		Options: []string{}, RequiredTools: []string{"python3"}, MinimumVersion: "3.11", Network: true,
		Requirements: "Python 3.11 or later with venv and pip. Manual dependency installation requires network access; Ubuntu may require python3-venv.",
		NameHint:     "1–64 lowercase letters, digits or hyphens; start and end with a letter or digit.",
	}, render: renderFastAPI}, {definition: Definition{
		ID: "nextjs", Name: "Next.js application", Description: "Use the official create-next-app scaffolder, then review generated files before publishing.",
		Options: []string{"next"}, RequiredTools: []string{"node", "npm"}, MinimumVersion: "20.9.0", Network: true,
		Requirements: "Node.js 20.9.0 or later, npm, and the selected package manager. Preparing files downloads and runs create-next-app 16.4.3; dependencies and Git setup are skipped.",
		NameHint:     "1–64 lowercase letters, digits or hyphens; start with a letter or digit; avoid npm reserved names.",
	}, render: renderNext}}}
}

func (registry Registry) Definition(id string) (Definition, error) {
	for _, definition := range registry.List() {
		if definition.ID == id {
			return definition, nil
		}
	}
	return Definition{}, ErrOptions
}

func (registry Registry) List() []Definition {
	result := make([]Definition, 0, len(registry.starters))
	for _, item := range registry.starters {
		definition := item.definition
		definition.Options = slices.Clone(definition.Options)
		definition.RequiredTools = slices.Clone(definition.RequiredTools)
		result = append(result, definition)
	}
	return result
}

func (registry Registry) Render(id string, options Options) (Plan, error) {
	if !safeName.MatchString(options.Name) {
		return Plan{}, ErrOptions
	}
	for _, item := range registry.starters {
		if item.definition.ID == id {
			return item.render(options)
		}
	}
	return Plan{}, ErrOptions
}

func renderGo(options Options) (Plan, error) {
	if len(options.ModulePath) > 512 || module.CheckPath(options.ModulePath) != nil {
		return Plan{}, ErrModulePath
	}
	return Plan{TemplateID: "go", Files: []File{
		{Path: "go.mod", Contents: "module " + options.ModulePath + "\n\ngo 1.22.0\n"},
		{Path: "main.go", Contents: "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(" + strconv.Quote("Hello from "+options.Name+"!") + ")\n}\n"},
		{Path: ".gitignore", Contents: "/bin/\n/coverage.out\n*.test\n.env\n.env.*\n!.env.example\n"},
		{Path: "README.md", Contents: "# " + options.Name + "\n\nRequires Go 1.22 or later. No third-party dependencies.\n\n```sh\ngo run .\ngo build -o bin/" + options.Name + " .\n```\n"},
	}, InitializationCommands: []string{}, StartupProgram: "go", StartupName: "Run Go application", StartupArguments: []string{"run", "."}, Preview: "Command-line output; no browser preview."}, nil
}
