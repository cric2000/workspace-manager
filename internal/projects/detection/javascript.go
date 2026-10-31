package detection

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
)

// Token validation catches duplicate keys (including nested objects) instead of
// silently choosing whichever package-manager/script declaration appears last.
func jsonObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 64 {
			return errors.New("JSON depth")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if delimiter, ok := token.(json.Delim); ok {
			seen := map[string]bool{}
			for decoder.More() {
				if delimiter == '{' {
					key, err := decoder.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok || seen[name] {
						return errors.New("duplicate JSON key")
					}
					seen[name] = true
				}
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		}
		return nil
	}
	if err := visit(0); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing JSON")
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil || result == nil {
		return nil, errors.New("JSON object required")
	}
	return result, nil
}

func (r *reader) javascript() {
	data, ok := r.read("package.json")
	if !ok {
		return
	}
	manifest, err := jsonObject(data)
	if err != nil {
		r.malformed("package.json")
		return
	}
	scripts, dependencies := map[string]string{}, map[string]string{}
	if value, ok := manifest["scripts"]; ok {
		if json.Unmarshal(value, &scripts) != nil {
			r.malformed("package.json")
			return
		}
	}
	for _, field := range []string{"dependencies", "devDependencies", "peerDependencies"} {
		if value, ok := manifest[field]; ok {
			var values map[string]string
			if json.Unmarshal(value, &values) != nil {
				r.malformed("package.json")
				return
			}
			for key, value := range values {
				dependencies[key] = value
			}
		}
	}
	language := "JavaScript"
	if _, ok := dependencies["typescript"]; ok || r.present("tsconfig.json") {
		language = "TypeScript"
	}
	frameworks := []string{}
	for _, pair := range [][2]string{{"next", "Next.js"}, {"vite", "Vite"}, {"react", "React"}, {"vue", "Vue"}, {"svelte", "Svelte"}, {"@angular/core", "Angular"}, {"astro", "Astro"}, {"express", "Express"}, {"fastify", "Fastify"}} {
		if _, ok := dependencies[pair[0]]; ok {
			frameworks = append(frameworks, pair[1])
		}
	}
	managers := map[string]bool{}
	evidence := []string{"package.json"}
	for _, pair := range [][2]string{{"package-lock.json", "npm"}, {"npm-shrinkwrap.json", "npm"}, {"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"}} {
		if r.present(pair[0]) {
			managers[pair[1]] = true
			evidence = append(evidence, pair[0])
		}
	}
	if value, ok := manifest["packageManager"]; ok {
		var declaration string
		if json.Unmarshal(value, &declaration) != nil {
			r.malformed("package.json")
			return
		}
		manager, _, _ := strings.Cut(declaration, "@")
		if !slices.Contains([]string{"npm", "pnpm", "yarn", "bun"}, manager) {
			r.warn("unknown_manager", "package.json", "The declared package manager is not supported. Choose a manager before configuring a startup command.")
		} else {
			managers[manager] = true
		}
	}
	managerNames := sortedKeys(managers)
	if len(managerNames) > 1 {
		r.warn("manager_conflict", "package.json", "Package-manager declarations or lockfiles disagree. Each shown manager is an alternative; choose the intended one before saving a command.")
	}
	if len(managerNames) == 0 {
		r.warn("manager_unknown", "package.json", "No supported package manager was established. Script names are shown, but no manager was silently selected.")
	}
	if _, ok := manifest["workspaces"]; ok {
		r.warn("workspace_packages", "package.json", "This manifest declares workspace packages. Only root scripts were inspected; select a package directory to inspect its own startup choices.")
	}
	entries := []string{}
	for _, name := range sortedKeys(scripts) {
		if !validName(name) || len(scripts[name]) == 0 {
			r.warn("unsupported_script", "package.json", "An empty or unusually named script was not proposed. Review it manually.")
			continue
		}
		if len(entries) >= 128 {
			r.limit("package.json")
			break
		}
		entries = append(entries, name)
		if !startupName(name) {
			continue
		}
		for _, manager := range managerNames {
			r.candidate(manager+": "+name, manager, []string{"run", name}, "This startup script is declared in package.json. Running it later may execute package lifecycle hooks; its body was not evaluated.", "declared")
		}
	}
	r.stack(language, frameworks, managerNames, evidence, entries)
}
