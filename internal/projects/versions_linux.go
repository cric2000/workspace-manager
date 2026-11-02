package projects

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/projects/bootstrap"
)

type ToolVersionCommand struct {
	Tool           string   `json:"tool"`
	Executable     string   `json:"executable"`
	Arguments      []string `json:"arguments"`
	MinimumVersion string   `json:"minimumVersion"`
}
type ToolVersionCheck struct {
	Tool           string `json:"tool"`
	Version        string `json:"version"`
	MinimumVersion string `json:"minimumVersion"`
	Status         string `json:"status"`
	Message        string `json:"message"`
}
type ToolVersionResult struct {
	Checks  []ToolVersionCheck `json:"checks"`
	Code    string             `json:"code"`
	Message string             `json:"message"`
}
type versionProbe struct {
	command    ToolVersionCommand
	node       string
	identities []fileIdentity
}

func newVersionProbe(tool, executable, minimum, node string, arguments []string) (versionProbe, error) {
	command := ToolVersionCommand{Tool: tool, Executable: executable, Arguments: arguments, MinimumVersion: minimum}
	ids, err := setupTools(SetupReview{Action: domain.CommandAction{Executable: executable, Arguments: arguments}}, node)
	return versionProbe{command: command, node: node, identities: ids}, err
}
func creationVersionProbes(definition bootstrap.Definition, plan bootstrap.Plan, tools map[string]string) ([]versionProbe, error) {
	probes := []versionProbe{}
	for _, tool := range bootstrap.SelectedTools(definition, plan) {
		minimum := ""
		if tool == "go" || tool == "node" || tool == "python3" {
			minimum = definition.MinimumVersion
		}
		arguments := []string{"--version"}
		if tool == "go" {
			arguments = []string{"version"}
		}
		if tool == "python3" {
			arguments = []string{"-I", "-B", "--version"}
		}
		executable := tools[tool]
		node := ""
		if tool == "npm" || tool == "pnpm" {
			node = tools["node"]
			if nodeScript(executable) {
				arguments = append([]string{executable}, arguments...)
				executable = node
			}
		}
		probe, err := newVersionProbe(tool, executable, minimum, node, arguments)
		if err != nil {
			return nil, err
		}
		probes = append(probes, probe)
	}
	return probes, nil
}
func nodeScript(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	prefix := make([]byte, 256)
	n, _ := file.Read(prefix)
	return bytes.HasPrefix(prefix[:n], []byte("#!")) && bytes.Contains(bytes.SplitN(prefix[:n], []byte("\n"), 2)[0], []byte("node"))
}
func setupVersionProbes(review SetupReview, template, node string) ([]versionProbe, error) {
	definition, err := bootstrap.NewRegistry().Definition(template)
	if err != nil {
		return nil, err
	}
	action := review.Action
	if review.Kind == "git" {
		probe, err := newVersionProbe("git", action.Executable, "2.28.0", "", []string{"--version"})
		return []versionProbe{probe}, err
	}
	if node != "" {
		probe, err := newVersionProbe("node", node, definition.MinimumVersion, "", []string{"--version"})
		if err != nil {
			return nil, err
		}
		arguments := []string{"--version"}
		if len(action.Arguments) > 0 && filepath.IsAbs(action.Arguments[0]) {
			arguments = append([]string{action.Arguments[0]}, arguments...)
		}
		tool := "npm"
		if len(action.Arguments) > 0 && (action.Arguments[0] == "--dir" || slices.Contains(action.Arguments, "--ignore-pnpmfile")) {
			tool = "pnpm"
		}
		manager, err := newVersionProbe(tool, action.Executable, "", node, arguments)
		return []versionProbe{probe, manager}, err
	}
	python, err := newVersionProbe("python3", action.Executable, definition.MinimumVersion, "", []string{"-I", "-B", "--version"})
	if err != nil {
		return nil, err
	}
	if review.Kind == "environment" {
		return []versionProbe{python}, nil
	}
	pip, err := newVersionProbe("pip", action.Executable, "", "", []string{"-I", "-B", "-m", "pip", "--isolated", "--version"})
	return []versionProbe{python, pip}, err
}
func versionCommands(probes []versionProbe) []ToolVersionCommand {
	commands := make([]ToolVersionCommand, len(probes))
	for i, probe := range probes {
		commands[i] = probe.command
		commands[i].Arguments = slices.Clone(probe.command.Arguments)
	}
	return commands
}
func cloneVersionCommands(commands []ToolVersionCommand) []ToolVersionCommand {
	commands = slices.Clone(commands)
	for i := range commands {
		commands[i].Arguments = slices.Clone(commands[i].Arguments)
	}
	return commands
}

// CheckCreationToolVersions is an explicit, advisory diagnostic. Reviewing or
// publishing files never runs a version command. Results do not grant approval.
func (service *Service) CheckCreationToolVersions(ctx context.Context, token string) ToolVersionResult {
	return service.checkToolVersions(ctx, token, false)
}
func (service *Service) CheckSetupToolVersions(ctx context.Context, token string) ToolVersionResult {
	return service.checkToolVersions(ctx, token, true)
}
func (service *Service) checkToolVersions(ctx context.Context, token string, setup bool) ToolVersionResult {
	service.mu.Lock()
	probes := []versionProbe(nil)
	var expires time.Time
	if setup {
		if pending := service.setups[token]; pending != nil && pending.result == nil {
			probes, expires = pending.probes, pending.review.ExpiresAt
		}
	} else if pending := service.creations[token]; pending != nil && pending.result == nil {
		probes, expires = pending.probes, pending.review.ExpiresAt
	}
	if service.closed || len(probes) == 0 || !service.now().Before(expires) {
		service.mu.Unlock()
		return ToolVersionResult{Code: "review_changed", Message: "This review is unavailable or expired. Request a new review before checking versions."}
	}
	if setup {
		pending := service.setups[token]
		directory, err := openSetupDirectory(pending.creation.project.Path)
		var inputs []fileIdentity
		if err == nil {
			inputs, err = setupSnapshot(pending.creation, pending.review.Kind, directory)
			directory.Close()
		}
		if err != nil || !slices.Equal(inputs, pending.inputs) {
			service.mu.Unlock()
			return ToolVersionResult{Code: "review_changed", Message: "Project setup inputs changed. Request a new review before checking versions."}
		}
	}
	if ctx.Err() != nil {
		service.mu.Unlock()
		return ToolVersionResult{Code: "cancelled", Message: "Version checks cancelled."}
	}
	select {
	case service.preparationSlot <- struct{}{}:
	default:
		service.mu.Unlock()
		return ToolVersionResult{Code: "busy", Message: "Another version check, setup or preparation is running. Wait, then check again."}
	}
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	service.versionCancel = cancel
	runner := service.versionRunner
	service.mu.Unlock()
	defer func() {
		cancel()
		service.mu.Lock()
		service.versionCancel = nil
		<-service.preparationSlot
		service.mu.Unlock()
	}()
	result := ToolVersionResult{Checks: []ToolVersionCheck{}, Message: "Version checks finished. Minimum versions are advisory; package-manager features and startup approval still require review."}
	for _, probe := range probes {
		check := ToolVersionCheck{Tool: probe.command.Tool, MinimumVersion: probe.command.MinimumVersion}
		action := domain.CommandAction{Executable: probe.command.Executable, Arguments: slices.Clone(probe.command.Arguments)}
		ids, err := setupTools(SetupReview{Action: action}, probe.node)
		switch {
		case runCtx.Err() != nil:
			check.Status, check.Message = "cancelled", "Version check cancelled or its total time budget was exceeded."
		case err != nil || !slices.Equal(ids, probe.identities):
			check.Status, check.Message = "changed", "The reviewed executable changed. Request a new review."
		default:
			executed := runner(runCtx, action, probe.node)
			ids, err = setupTools(SetupReview{Action: action}, probe.node)
			switch {
			case err != nil || !slices.Equal(ids, probe.identities):
				check.Status, check.Message = "changed", "The executable changed during its check. Request a new review."
			case errors.Is(executed.Err, context.DeadlineExceeded):
				check.Status, check.Message = "timeout", "Version command exceeded its time budget."
			case errors.Is(executed.Err, context.Canceled) || runCtx.Err() != nil:
				check.Status, check.Message = "cancelled", "Version check cancelled."
			case executed.Err != nil || executed.ExitCode != 0:
				check.Status, check.Message = "failed", "Version command failed. Check the executable installation; no project setup was started."
			case executed.Truncated:
				check.Status, check.Message = "unknown", "Version output exceeded the 4 KiB limit."
			default:
				check.Version = parseToolVersion(check.Tool, executed.Output)
				if check.Version == "" {
					check.Status, check.Message = "unknown", "Unrecognized or prerelease version; compatibility could not be confirmed."
				} else if check.MinimumVersion == "" {
					check.Status, check.Message = "detected", "Version detected; no minimum is declared for this tool."
				} else if versionAtLeast(check.Version, check.MinimumVersion) {
					check.Status, check.Message = "supported", "Meets the starter's declared minimum version."
				} else {
					check.Status, check.Message = "unsupported", "Below the required minimum. Update this tool and request a new review before setup."
				}
			}
		}
		result.Checks = append(result.Checks, check)
		if check.Status != "supported" && check.Status != "detected" {
			result.Code = "version_attention"
			result.Message = "Some tool versions could not be confirmed or do not meet requirements. Review the results before setup."
		}
	}
	return result
}

var stableVersion = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

func parseToolVersion(tool, output string) string {
	fields := strings.Fields(strings.TrimSpace(output))
	version := ""
	switch tool {
	case "go":
		if len(fields) == 4 && fields[0] == "go" && fields[1] == "version" && strings.HasPrefix(fields[2], "go") {
			version = strings.TrimPrefix(fields[2], "go")
		}
	case "node":
		if len(fields) == 1 && strings.HasPrefix(fields[0], "v") {
			version = strings.TrimPrefix(fields[0], "v")
		}
	case "python3":
		if len(fields) == 2 && fields[0] == "Python" {
			version = fields[1]
		}
	case "git":
		if len(fields) == 3 && fields[0] == "git" && fields[1] == "version" {
			version = fields[2]
		}
	case "pip":
		if len(fields) >= 5 && fields[0] == "pip" && fields[2] == "from" {
			version = fields[1]
			if strings.Count(version, ".") == 1 {
				version += ".0"
			}
		}
	case "npm", "pnpm":
		if len(fields) == 1 {
			version = fields[0]
		}
	}
	if !stableVersion.MatchString(version) {
		return ""
	}
	return version
}
func versionAtLeast(version, minimum string) bool {
	actual, required := strings.Split(version, "."), strings.Split(minimum, ".")
	for len(required) < 3 {
		required = append(required, "0")
	}
	for i := 0; i < 3; i++ {
		a, _ := strconv.Atoi(actual[i])
		b, _ := strconv.Atoi(required[i])
		if a != b {
			return a > b
		}
	}
	return true
}
