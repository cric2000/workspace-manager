package domain

import (
	"strings"
	"testing"
)

func TestBootstrapRecoveryValidatesBoundedUnapprovedCheckpoint(t *testing.T) {
	project := validProject()
	project.Source = SourceBootstrapped
	base := BootstrapRecovery{ID: workspaceID, ProjectID: project.ID, Path: project.Path, Kind: "publication", RecordedAt: testTime, Project: &project, Files: []RecoveryFile{{Path: "main.go", Size: 4, SHA256: strings.Repeat("a", 64)}}}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"traversal", "hash", "overflow", "duplicate", "collision", "wrong-kind", "approval", "metadata"} {
		t.Run(scenario, func(t *testing.T) {
			value := base
			value.Files = append([]RecoveryFile{}, base.Files...)
			copyProject := project
			value.Project = &copyProject
			switch scenario {
			case "traversal":
				value.Files[0].Path = "../outside"
			case "hash":
				value.Files[0].SHA256 = "not-a-hash"
			case "overflow":
				value.Files[0].Size = 1 << 62
			case "duplicate":
				value.Files = append(value.Files, value.Files[0])
			case "collision":
				value.Files = append(value.Files, RecoveryFile{Path: "main.go/nested", SHA256: strings.Repeat("b", 64)})
			case "wrong-kind":
				value.Kind = "dependencies"
			case "approval":
				configuration := CommandConfiguration{Name: "Run", Executable: "/usr/bin/go", Arguments: []string{"run", "."}, WorkingDirectory: project.Path}
				copyProject.Commands = []StartupCommand{{ID: sessionID, Configuration: configuration, Approval: &CommandApproval{Fingerprint: configuration.Fingerprint(), ApprovedAt: testTime}}}
			case "metadata":
				copyProject.Path = "/different"
			}
			if err := value.Validate(); err == nil {
				t.Fatal("unsafe checkpoint accepted", scenario)
			}
		})
	}
}
