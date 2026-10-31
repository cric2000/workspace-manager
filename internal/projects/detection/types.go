// Package detection inspects bounded local metadata. It never invokes project
// tools, imports modules, evaluates recipes, installs dependencies or writes files.
package detection

import "context"

type Finding struct {
	Code    string `json:"code"`
	File    string `json:"file"`
	Message string `json:"message"`
}
type Stack struct {
	Language        string   `json:"language"`
	Frameworks      []string `json:"frameworks"`
	PackageManagers []string `json:"packageManagers"`
	Evidence        []string `json:"evidence"`
	EntryPoints     []string `json:"entryPoints"`
}

// Candidate is a suggestion, not an approved/persistent command. Program is a
// tool name; approval must resolve its actual executable and review configuration.
type Candidate struct {
	Label            string   `json:"label"`
	Program          string   `json:"program"`
	Arguments        []string `json:"arguments"`
	WorkingDirectory string   `json:"workingDirectory"`
	Reason           string   `json:"reason"`
	Confidence       string   `json:"confidence"`
}
type Tool struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
}
type Report struct {
	Path       string      `json:"path"`
	Stacks     []Stack     `json:"stacks"`
	Candidates []Candidate `json:"candidates"`
	Tools      []Tool      `json:"tools"`
	Findings   []Finding   `json:"findings"`
	Partial    bool        `json:"partial"`
}
type Inspector interface {
	Inspect(context.Context, string) (Report, error)
}
