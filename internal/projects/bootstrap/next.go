package bootstrap

import (
	"strings"

	"workspace-manager/internal/domain"
)

const NextScaffolderVersion = "16.4.3"

type NextOptions struct {
	TypeScript      bool   `json:"typeScript"`
	Tailwind        bool   `json:"tailwind"`
	AppRouter       bool   `json:"appRouter"`
	SourceDirectory bool   `json:"sourceDirectory"`
	PackageManager  string `json:"packageManager"`
}

func renderNext(options Options) (Plan, error) {
	if !nodeName.MatchString(options.Name) || options.Name == "node_modules" || options.Name == "favicon.ico" || options.Name == "next" || options.Name == "react" || options.Name == "react-dom" || options.Name == "npm" {
		return Plan{}, ErrNodeName
	}
	selected := NextOptions{TypeScript: true, Tailwind: true, AppRouter: true, SourceDirectory: true, PackageManager: "npm"}
	if options.Next != nil {
		selected = *options.Next
	}
	switch selected.PackageManager {
	case "npm", "pnpm":
	default:
		return Plan{}, ErrOptions
	}
	flag := func(enabled bool, name string) string {
		if enabled {
			return "--" + name
		}
		return "--no-" + name
	}
	language := "--js"
	if selected.TypeScript {
		language = "--ts"
	}
	args := []string{"exec", "--yes", "--ignore-scripts", "--package=create-next-app@" + NextScaffolderVersion, "--", "create-next-app", options.Name, language, flag(selected.Tailwind, "tailwind"), flag(selected.AppRouter, "app"), flag(selected.SourceDirectory, "src-dir"), "--use-" + selected.PackageManager, "--eslint", "--import-alias", "@/*", "--empty", "--no-react-compiler", "--no-cache-components", "--no-agents-md", "--no-agent-feedback", "--skip-install", "--disable-git", "--yes"}
	installArgs := []string{"install", "--ignore-scripts"}
	startupArgs := []string{"run", "dev", "--", "--hostname", "127.0.0.1", "--port", "3000"}
	if selected.PackageManager == "pnpm" {
		startupArgs = []string{"run", "dev", "--hostname", "127.0.0.1", "--port", "3000"}
	}
	return Plan{TemplateID: "nextjs", Files: []File{}, InitializationCommands: []string{}, StartupName: "Run Next.js development server", StartupProgram: selected.PackageManager, StartupArguments: startupArgs, Preview: "Local app at http://127.0.0.1:3000 after dependency setup and an approved start. Port 3000 must be free.", PreviewURL: "http://127.0.0.1:3000", Runtime: &domain.CommandRuntime{Readiness: &domain.ReadinessCheck{Kind: "http", URL: "http://127.0.0.1:3000", ExpectedStatus: 200, TimeoutSeconds: 120, IntervalSeconds: 1}, Stop: &domain.StopPolicy{Signal: "SIGTERM", GraceSeconds: 10}}, Scaffold: &Setup{Program: "npm", Arguments: args, Label: "Official Next.js scaffolder", Note: "Downloads and executes pinned create-next-app " + NextScaffolderVersion + " in a new private temporary directory. Uses its built-in empty starter; skips dependencies, Git initialization and agent files/feedback. Generated files are reviewed before the destination is created."}, DependencySetup: &Setup{Program: selected.PackageManager, Arguments: installArgs, Label: "Install Next.js dependencies", Note: "Manual step after publication: downloads dependencies and creates a lockfile. Lifecycle scripts are skipped. Review any required native build scripts separately. Not run by preparation or creation."}}, nil
}

// SelectedTools adds a template's chosen package manager without exposing
// arbitrary executable names from a client draft.
func SelectedTools(definition Definition, plan Plan) []string {
	result := append([]string{}, definition.RequiredTools...)
	for _, program := range []string{plan.StartupProgram} {
		if program != "" && !strings.Contains(program, "/") {
			found := false
			for _, current := range result {
				if current == program {
					found = true
				}
			}
			if !found {
				result = append(result, program)
			}
		}
	}
	return result
}
