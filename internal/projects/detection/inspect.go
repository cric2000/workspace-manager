package detection

import (
	"context"
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

type LocalInspector struct{}

func (LocalInspector) Inspect(ctx context.Context, path string) (Report, error) {
	report := Report{Stacks: []Stack{}, Candidates: []Candidate{}, Tools: []Tool{}, Findings: []Finding{}}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	reader, err := openReader(ctx, path, &report)
	if err != nil {
		return Report{}, err
	}
	defer reader.root.Close()
	reader.javascript()
	reader.golang()
	reader.python()
	reader.rust()
	reader.composeAndMake()
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	if len(report.Stacks) == 0 {
		reader.warn("no_metadata", "", "No supported project metadata was recognized. Choose the project directory containing its manifest, or configure commands manually in a later import step.")
	}
	languages := map[string]bool{}
	for _, stack := range report.Stacks {
		languages[stack.Language] = true
	}
	if len(languages) > 1 {
		reader.warn("mixed_stack", "", "Several project types were detected. Candidates are alternatives or separate services, not an automatic launch plan.")
	}
	tools := map[string]bool{}
	for _, candidate := range report.Candidates {
		tools[candidate.Program] = true
	}
	for _, stack := range report.Stacks {
		switch stack.Language {
		case "Go":
			tools["go"] = true
		case "Rust":
			tools["cargo"] = true
		case "Python":
			tools["python3"] = true
		case "Make":
			tools["make"] = true
		}
		for _, manager := range stack.PackageManagers {
			if slices.Contains([]string{"npm", "pnpm", "yarn", "bun"}, manager) {
				tools[manager] = true
			}
		}
	}
	for _, name := range sortedKeys(tools) {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		// LookPath only inspects filesystem entries; no tool/version invocation.
		_, err := exec.LookPath(name)
		report.Tools = append(report.Tools, Tool{Name: name, Available: err == nil})
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	return report, nil
}

func (r *reader) candidate(label, program string, args []string, reason, confidence string) {
	if len(r.report.Candidates) >= 64 {
		r.limit("")
		return
	}
	r.report.Candidates = append(r.report.Candidates, Candidate{label, program, args, r.report.Path, reason, confidence})
}
func (r *reader) stack(language string, frameworks, managers, evidence, entries []string) {
	if len(entries) > 128 {
		entries = entries[:128]
		file := ""
		if len(evidence) > 0 {
			file = evidence[0]
		}
		r.limit(file)
	}
	if frameworks == nil {
		frameworks = []string{}
	}
	if managers == nil {
		managers = []string{}
	}
	if entries == nil {
		entries = []string{}
	}
	r.report.Stacks = append(r.report.Stacks, Stack{language, frameworks, managers, evidence, entries})
}
func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

var simpleName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,119}$`)

func validName(value string) bool { return simpleName.MatchString(value) }
func (r *reader) malformed(file string) {
	r.report.Partial = true
	r.warn("malformed", file, "Metadata is malformed or has an unsupported field type. No command was inferred from it.")
}
func startupName(name string) bool {
	return slices.Contains([]string{"dev", "start", "serve", "run"}, strings.ToLower(name))
}
