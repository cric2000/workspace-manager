package domain

import (
	"strings"
	"testing"
	"time"
)

func approvedCommand() StartupCommand {
	configuration := CommandConfiguration{Name: "API", Executable: "/usr/bin/go", Arguments: []string{"run", "two words", "", "; $HOME"}, WorkingDirectory: "/code/api"}
	return StartupCommand{ID: workspaceID, Configuration: configuration, Approval: &CommandApproval{Fingerprint: configuration.Fingerprint(), ApprovedAt: testTime}}
}

func TestApprovalBindsEveryConfigurationField(t *testing.T) {
	command := approvedCommand()
	roundTrip(t, command)
	for _, change := range []func(*StartupCommand){
		func(c *StartupCommand) { c.Configuration.Name = "Other" },
		func(c *StartupCommand) { c.Configuration.Executable = "/usr/bin/other" },
		func(c *StartupCommand) { c.Configuration.WorkingDirectory = "/code/other" },
		func(c *StartupCommand) { c.Configuration.Arguments[0] = "changed" },
		func(c *StartupCommand) { c.Approval.ApprovedAt = time.Time{} },
	} {
		copy := CloneCommands([]StartupCommand{command})[0]
		change(&copy)
		if copy.Validate() == nil {
			t.Fatal("changed configuration retained valid approval")
		}
	}
	command.Approval = nil
	if err := command.Validate(); err != nil {
		t.Fatal("unapproved configuration is representable", err)
	}
	command.Configuration.Arguments = nil
	fingerprint := command.Configuration.Fingerprint()
	command.Configuration.Arguments = []string{}
	if fingerprint != command.Configuration.Fingerprint() {
		t.Fatal("empty argv normalization differs")
	}
}

func TestCommandBoundsAndIndependentSessionBranch(t *testing.T) {
	for _, change := range []func(*StartupCommand){
		func(c *StartupCommand) { c.Configuration.Executable = "go" },
		func(c *StartupCommand) { c.Configuration.WorkingDirectory = "relative" },
		func(c *StartupCommand) { c.Configuration.Arguments = make([]string, 129) },
		func(c *StartupCommand) { c.Configuration.Arguments = []string{strings.Repeat("a", 8193)} },
		func(c *StartupCommand) { c.Configuration.Arguments = []string{"\x00"} },
	} {
		command := approvedCommand()
		command.Approval = nil
		change(&command)
		if command.Validate() == nil {
			t.Fatal("accepted invalid command")
		}
	}
	session := validSession()
	session.Projects[0].Commands = []StartupCommand{approvedCommand()}
	branch, err := session.Branch("Independent", testTime)
	if err != nil {
		t.Fatal(err)
	}
	branch.Projects[0].Commands[0].Configuration.Arguments[0] = "other"
	branch.Projects[0].Commands[0].Approval.Fingerprint = "other"
	if err := session.Validate(); err != nil {
		t.Fatal("branch changed parent command or approval", err)
	}
	project := session.Projects[0]
	project.Commands = append(project.Commands, project.Commands[0])
	if project.Validate() == nil {
		t.Fatal("duplicate command ID accepted")
	}
}
