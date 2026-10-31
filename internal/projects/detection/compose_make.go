package detection

import (
	"bytes"
	"io"
	"strings"

	"go.yaml.in/yaml/v3"
)

func (r *reader) composeAndMake() {
	composeFiles := []string{}
	for _, file := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"} {
		data, ok := r.read(file)
		if !ok {
			continue
		}
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		var document, extra yaml.Node
		if decoder.Decode(&document) != nil || decoder.Decode(&extra) != io.EOF || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
			r.malformed(file)
			continue
		}
		root := document.Content[0]
		keys, entries := map[string]bool{}, []string{}
		valid := true
		for i := 0; i < len(root.Content); i += 2 {
			key, value := root.Content[i], root.Content[i+1]
			if key.Tag != "!!str" || keys[key.Value] {
				valid = false
				break
			}
			keys[key.Value] = true
			if key.Value != "services" {
				continue
			}
			if value.Kind != yaml.MappingNode {
				valid = false
				break
			}
			services := map[string]bool{}
			for j := 0; j < len(value.Content); j += 2 {
				name := value.Content[j]
				if name.Tag != "!!str" || !validName(name.Value) || services[name.Value] || value.Content[j+1].Kind != yaml.MappingNode {
					valid = false
					break
				}
				services[name.Value] = true
			}
			entries = sortedKeys(services)
		}
		if !valid || len(entries) == 0 {
			r.malformed(file)
			continue
		}
		if len(entries) > 128 {
			entries = entries[:128]
			r.limit(file)
		}
		composeFiles = append(composeFiles, file)
		r.stack("Docker Compose", nil, nil, []string{file}, entries)
		r.candidate("Compose: "+file, "docker", []string{"compose", "--file", file, "up"}, "This file declares Compose services. Schema compatibility, interpolation, images, includes and Docker/Compose availability were not evaluated.", "metadata")
	}
	if len(composeFiles) > 1 {
		r.warn("multiple_compose", "", "Multiple Compose files were found. Candidates select a file explicitly; their merge/override relationship was not assumed.")
	}
	if data, ok := r.read("Makefile"); ok {
		targets := map[string]bool{}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "#") || strings.Contains(line, "\\") {
				continue
			}
			head, tail, ok := strings.Cut(line, ":")
			if !ok || strings.HasPrefix(strings.TrimLeft(tail, ":"), "=") {
				continue
			}
			names := strings.Fields(head)
			valid := len(names) > 0
			for _, name := range names {
				if !validName(name) || strings.HasPrefix(name, ".") || strings.Contains(name, ":") {
					valid = false
				}
			}
			if !valid {
				continue
			}
			for _, name := range names {
				if len(targets) >= 128 {
					r.limit("Makefile")
					break
				}
				targets[name] = true
			}
		}
		entries := sortedKeys(targets)
		for _, name := range entries {
			if startupName(name) {
				r.candidate("Make: "+name, "make", []string{"-f", "Makefile", name}, "A simple explicit Make target was found. Recipes, variables, includes, conditionals and shell functions were not evaluated.", "convention")
			}
		}
		r.stack("Make", nil, nil, []string{"Makefile"}, entries)
		r.warn("make_static_only", "Makefile", "Only simple explicit target names were inspected. Make recipes, includes and conditionals were not evaluated, so targets may be conditional or incomplete.")
	}
}
