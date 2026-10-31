package domain

import (
	"strings"
	"testing"
)

func TestCustomApplicationValidationAndDeepCopy(t *testing.T) {
	custom := CustomApplication{ID: ID(strings.Repeat("a", 32)), Name: "Editor", Executable: "/opt/Editor App/editor", Arguments: []string{"two words", "", "; $HOME"}, WorkingDirectory: "/home/example/projects", InstanceBusName: "org.example.Editor"}
	for _, change := range []func(*CustomApplication){
		func(a *CustomApplication) { a.ID = "bad" }, func(a *CustomApplication) { a.Name = "" },
		func(a *CustomApplication) { a.Executable = "editor" }, func(a *CustomApplication) { a.WorkingDirectory = "~/projects" },
		func(a *CustomApplication) { a.Arguments = []string{"nul\x00"} }, func(a *CustomApplication) { a.Arguments = make([]string, 129) },
		func(a *CustomApplication) { a.Arguments = []string{strings.Repeat("x", 8193)} },
		func(a *CustomApplication) {
			a.Arguments = make([]string, 5)
			for i := range a.Arguments {
				a.Arguments[i] = strings.Repeat("x", 8192)
			}
		},
		func(a *CustomApplication) { a.InstanceBusName = ":1.2" }, func(a *CustomApplication) { a.InstanceBusName = "org.freedesktop.DBus" },
	} {
		copy := custom
		change(&copy)
		if copy.Validate() == nil {
			t.Fatal("invalid custom configuration accepted", copy)
		}
	}
	workspace := validWorkspace()
	workspace.Applications = []WorkspaceApplication{{Custom: &custom, DuplicatePolicy: SkipIfRunning}}
	roundTrip(t, workspace)
	copy := CloneApplications(workspace.Applications)
	copy[0].Custom.Name = "Changed"
	copy[0].Custom.Arguments[0] = "Changed"
	if custom.Name != "Editor" || custom.Arguments[0] != "two words" {
		t.Fatal("custom configuration shares mutable data")
	}
	workspace.Applications[0].DesktopID = "editor.desktop"
	if workspace.Validate() == nil {
		t.Fatal("ambiguous application target accepted")
	}
	workspace.Applications[0].DesktopID = ""
	workspace.Applications = append(workspace.Applications, workspace.Applications[0])
	if workspace.Validate() == nil {
		t.Fatal("duplicate custom identity accepted")
	}
}
