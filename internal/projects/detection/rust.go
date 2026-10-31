package detection

import (
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

func (r *reader) rust() {
	data, ok := r.read("Cargo.toml")
	if !ok {
		return
	}
	var manifest struct {
		Package *struct {
			Name       string
			Edition    string
			Autobins   *bool
			DefaultRun string `toml:"default-run"`
		}
		Bin []struct {
			Name             string
			Path             string
			RequiredFeatures []string `toml:"required-features"`
		}
		Workspace            map[string]any
		Lib                  map[string]any
		Test, Example, Bench []map[string]any
	}
	if toml.Unmarshal(data, &manifest) != nil {
		r.malformed("Cargo.toml")
		return
	}
	if manifest.Package == nil {
		r.stack("Rust", nil, []string{"Cargo"}, []string{"Cargo.toml"}, nil)
		r.warn("cargo_workspace", "Cargo.toml", "No package was declared in this manifest. Select a workspace member; the workspace root was not assumed to be runnable.")
		return
	}
	targets, blocked := map[string]bool{}, map[string]bool{}
	for _, target := range manifest.Bin {
		if !validName(target.Name) {
			r.warn("cargo_target", "Cargo.toml", "A binary target has no supported explicit name; choose its name/path manually.")
			continue
		}
		if len(target.RequiredFeatures) > 0 {
			blocked[target.Name] = true
			r.warn("cargo_features", "Cargo.toml", "A binary requires feature flags. It was not proposed without an explicit feature selection.")
			continue
		}
		if target.Path != "" && (!filepath.IsLocal(target.Path) || !r.present(target.Path)) {
			blocked[target.Name] = true
			r.warn("cargo_target_path", "Cargo.toml", "A declared binary path is unavailable or outside the selected directory. Its command was not proposed.")
			continue
		}
		targets[target.Name] = true
	}
	auto := manifest.Package.Autobins == nil || *manifest.Package.Autobins
	if manifest.Package.Autobins == nil && (manifest.Package.Edition == "" || manifest.Package.Edition == "2015") && (len(manifest.Bin) > 0 || manifest.Lib != nil || len(manifest.Test)+len(manifest.Example)+len(manifest.Bench) > 0) {
		auto = false
	}
	if auto {
		if validName(manifest.Package.Name) && r.present("src/main.rs") {
			targets[manifest.Package.Name] = true
		}
		for _, entry := range r.directory("src/bin") {
			name := entry.Name()
			if entry.IsDir() {
				if validName(name) && r.present("src/bin/"+name+"/main.rs") {
					targets[name] = true
				}
			} else if strings.HasSuffix(name, ".rs") {
				name = strings.TrimSuffix(name, ".rs")
				if validName(name) && r.present("src/bin/"+entry.Name()) {
					targets[name] = true
				}
			}
		}
	}
	for name := range blocked {
		delete(targets, name)
	}
	entries := sortedKeys(targets)
	for _, name := range entries {
		r.candidate("Cargo: "+name, "cargo", []string{"run", "--bin", name}, "A Cargo binary target was declared or found by standard auto-discovery. Compilation, features and runtime behavior were not verified.", "metadata")
	}
	if len(entries) == 0 {
		r.warn("no_cargo_bin", "Cargo.toml", "No runnable Cargo binary was established. This may be a library or require an explicit target/feature configuration.")
	}
	if len(entries) > 1 {
		r.warn("multiple_cargo_bins", "Cargo.toml", "Multiple Cargo binaries were found. Each candidate names its binary explicitly; no default target was silently chosen.")
	}
	r.stack("Rust", nil, []string{"Cargo"}, []string{"Cargo.toml"}, entries)
}
