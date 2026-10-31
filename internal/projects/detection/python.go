package detection

import (
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

var pythonReference = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*:[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
var requirementName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*`)

func (r *reader) python() {
	evidence, entries := []string{}, []string{}
	dependencies, managers := map[string]bool{}, map[string]bool{}
	console := map[string]string{}
	fastapiEntry := ""
	addDependency := func(value string) {
		name := requirementName.FindString(value)
		if name != "" {
			dependencies[strings.NewReplacer("_", "-", ".", "-").Replace(strings.ToLower(name))] = true
		}
	}
	if data, ok := r.read("pyproject.toml"); ok {
		evidence = append(evidence, "pyproject.toml")
		var manifest struct {
			Project struct {
				Dependencies []string
				Scripts      map[string]string
			}
			Tool struct {
				Poetry struct {
					Dependencies map[string]any
					Scripts      map[string]string
				}
				FastAPI struct {
					EntryPoint string `toml:"entrypoint"`
				}
			}
		}
		if toml.Unmarshal(data, &manifest) != nil {
			r.malformed("pyproject.toml")
		} else {
			for _, dependency := range manifest.Project.Dependencies {
				addDependency(dependency)
			}
			for name := range manifest.Tool.Poetry.Dependencies {
				addDependency(name)
			}
			for name, reference := range manifest.Project.Scripts {
				console[name] = reference
			}
			for name, reference := range manifest.Tool.Poetry.Scripts {
				if old, exists := console[name]; exists && old != reference {
					r.warn("python_script_conflict", "pyproject.toml", "Python script declarations disagree. Conflicting console entrypoints were not proposed.")
					console[name] = ""
				} else {
					console[name] = reference
				}
			}
			fastapiEntry = manifest.Tool.FastAPI.EntryPoint
		}
	}
	if data, ok := r.read("requirements.txt"); ok {
		evidence = append(evidence, "requirements.txt")
		managers["pip"] = true
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, "-") || strings.Contains(line, "://") {
				r.warn("requirements_indirect", "requirements.txt", "Requirement includes, editable installs and URLs were not followed. Dependency detection may be incomplete.")
				continue
			}
			addDependency(line)
		}
	}
	for _, pair := range [][2]string{{"uv.lock", "uv"}, {"poetry.lock", "Poetry"}, {"pdm.lock", "PDM"}} {
		if r.present(pair[0]) {
			evidence = append(evidence, pair[0])
			managers[pair[1]] = true
		}
	}
	if r.present("manage.py") {
		evidence = append(evidence, "manage.py")
		entries = append(entries, "manage.py")
		r.candidate("Python: manage.py runserver", "python3", []string{"manage.py", "runserver"}, "A root manage.py exists. This is a conventional Django entrypoint; its code and Python environment were not executed or verified.", "convention")
	}
	if len(evidence) == 0 {
		return
	}
	frameworks := []string{}
	for _, pair := range [][2]string{{"fastapi", "FastAPI"}, {"django", "Django"}, {"flask", "Flask"}} {
		if dependencies[pair[0]] {
			frameworks = append(frameworks, pair[1])
		}
	}
	if fastapiEntry != "" {
		if len(fastapiEntry) <= 240 && pythonReference.MatchString(fastapiEntry) {
			if !slices.Contains(frameworks, "FastAPI") {
				frameworks = append(frameworks, "FastAPI")
			}
			entries = append(entries, fastapiEntry)
			r.candidate("FastAPI: "+fastapiEntry, "fastapi", []string{"dev", "--entrypoint", fastapiEntry}, "The FastAPI entrypoint is explicitly configured in pyproject.toml. Imports, dependencies and environment were not checked.", "declared")
		} else {
			r.warn("python_entrypoint", "pyproject.toml", "The configured FastAPI entrypoint is unsupported. No module/object name was guessed.")
		}
	} else if dependencies["fastapi"] {
		r.warn("python_entrypoint", "pyproject.toml", "FastAPI is declared, but no explicit ASGI entrypoint was established. Source files were not imported or guessed; configure the entrypoint during review.")
	}
	for _, name := range sortedKeys(console) {
		if len(entries) >= 128 {
			r.limit("pyproject.toml")
			break
		}
		if !validName(name) || strings.Contains(name, ":") || len(console[name]) > 240 || !pythonReference.MatchString(console[name]) {
			r.warn("python_console", "pyproject.toml", "An unsupported console entrypoint was not proposed.")
			continue
		}
		entries = append(entries, name+" → "+console[name])
		r.candidate("Python console: "+name, name, []string{}, "This console entrypoint is declared in pyproject.toml. It must already be installed in the intended Python environment; no installation was attempted.", "declared")
	}
	if len(entries) == 0 {
		r.warn("python_entrypoint_unknown", "", "Python metadata was found without an established startup entrypoint. Choose the environment and application entrypoint explicitly.")
	}
	r.stack("Python", frameworks, sortedKeys(managers), evidence, entries)
}
