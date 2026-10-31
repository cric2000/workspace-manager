package domain

import (
	"fmt"
	"strings"
	"testing"
)

func TestWorkspaceApplicationsValidateIdentityPolicyAndOrder(t *testing.T) {
	workspace := validWorkspace()
	workspace.Applications = []WorkspaceApplication{{DesktopID: "org.example.Editor.desktop", DuplicatePolicy: SkipIfRunning}, {DesktopID: "org.example.Browser.desktop", DuplicatePolicy: AlwaysLaunch}}
	roundTrip(t, workspace)
	for _, change := range []func(*Workspace){
		func(w *Workspace) { w.Applications[0].DesktopID = "/tmp/unknown.desktop" },
		func(w *Workspace) { w.Applications[0].DuplicatePolicy = "" },
		func(w *Workspace) { w.Applications[0].DuplicatePolicy = "kill_existing" },
		func(w *Workspace) { w.Applications[1].DesktopID = w.Applications[0].DesktopID },
	} {
		candidate := workspace
		candidate.Applications = append([]WorkspaceApplication{}, workspace.Applications...)
		change(&candidate)
		if err := candidate.Validate(); err == nil || !strings.Contains(err.Error(), "applications[") {
			t.Fatal("invalid application settings accepted", err)
		}
	}
	workspace.Applications = nil
	for index := range MaxWorkspaceApplications {
		workspace.Applications = append(workspace.Applications, WorkspaceApplication{DesktopID: DesktopID(fmt.Sprintf("org.example.App%d.desktop", index)), DuplicatePolicy: SkipIfRunning})
	}
	if err := workspace.Validate(); err != nil {
		t.Fatal(err)
	}
	workspace.Applications = append(workspace.Applications, WorkspaceApplication{DesktopID: "org.example.Excess.desktop", DuplicatePolicy: AlwaysLaunch})
	if err := workspace.Validate(); err == nil {
		t.Fatal("application limit not enforced")
	}
}
