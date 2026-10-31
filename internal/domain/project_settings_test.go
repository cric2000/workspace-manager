package domain

import (
	"testing"
)

func runtimeFixture() *CommandRuntime {
	return &CommandRuntime{EnvironmentFiles: []string{".env", "config/local.env"}, DelaySeconds: 3, Readiness: &ReadinessCheck{Kind: "http", URL: "http://127.0.0.1:8123/health", ExpectedStatus: 204, TimeoutSeconds: 30, IntervalSeconds: 1}, Stop: &StopPolicy{Signal: "SIGINT", GraceSeconds: 12, Action: &CommandAction{Executable: "/usr/bin/docker", Arguments: []string{"compose", "down", ""}, WorkingDirectory: "/code/api"}}}
}

func TestRuntimeApprovalBindsAllSettings(t *testing.T) {
	command := approvedCommand()
	command.Configuration.Runtime = runtimeFixture()
	command.Approval.Fingerprint = command.Configuration.Fingerprint()
	roundTrip(t, command)
	for _, change := range []func(*CommandRuntime){
		func(r *CommandRuntime) { r.EnvironmentFiles[0] = "different.env" },
		func(r *CommandRuntime) {
			r.EnvironmentFiles[0], r.EnvironmentFiles[1] = r.EnvironmentFiles[1], r.EnvironmentFiles[0]
		},
		func(r *CommandRuntime) { r.DelaySeconds++ },
		func(r *CommandRuntime) { r.Readiness.URL = "http://localhost:8123/other" },
		func(r *CommandRuntime) { r.Readiness.ExpectedStatus++ },
		func(r *CommandRuntime) { r.Readiness.TimeoutSeconds++ },
		func(r *CommandRuntime) { r.Readiness.IntervalSeconds++ },
		func(r *CommandRuntime) { r.Stop.Signal = "SIGTERM" },
		func(r *CommandRuntime) { r.Stop.GraceSeconds++ },
		func(r *CommandRuntime) { r.Stop.Action.Executable = "/usr/bin/other" },
		func(r *CommandRuntime) { r.Stop.Action.Arguments[0] = "other" },
		func(r *CommandRuntime) { r.Stop.Action.WorkingDirectory = "/code/other" },
	} {
		copy := CloneCommands([]StartupCommand{command})[0]
		change(copy.Configuration.Runtime)
		if copy.Validate() == nil {
			t.Fatal("setting change retained approval")
		}
		if command.Validate() != nil {
			t.Fatal("copy mutated original runtime")
		}
	}
}

func TestReadinessAndURLValidation(t *testing.T) {
	for _, url := range []string{"http://localhost:8123/health", "https://[::1]:8443/health", "https://docs.example.test/api#usage"} {
		if err := ValidateWebURL(url); err != nil {
			t.Fatal(url, err)
		}
	}
	for _, url := range []string{"", "relative", "javascript:alert(1)", "file:///tmp/test", "http://user:secret@localhost/", "http://localhost:0/", "http://localhost:65536/", "http://localhost:/", "http://localhost:bad/", "https:///missing", "http://localhost/\n"} {
		if ValidateWebURL(url) == nil {
			t.Fatal("accepted URL", url)
		}
	}
	for _, check := range []ReadinessCheck{
		{Kind: "tcp", Host: "::1", Port: 8123, TimeoutSeconds: 1, IntervalSeconds: 1},
		{Kind: "http", URL: "http://127.0.0.2:8123/health", ExpectedStatus: 200, TimeoutSeconds: 300, IntervalSeconds: 30},
	} {
		if err := check.Validate(); err != nil {
			t.Fatal(check, err)
		}
	}
	for _, change := range []func(*ReadinessCheck){
		func(c *ReadinessCheck) { c.URL = "https://public.example.test/health" },
		func(c *ReadinessCheck) { c.URL = "http://localhost.evil.test/health" },
		func(c *ReadinessCheck) { c.URL = "http://localhost/health#fragment" },
		func(c *ReadinessCheck) { c.Kind = "command" },
		func(c *ReadinessCheck) { c.TimeoutSeconds = 301 },
		func(c *ReadinessCheck) { c.IntervalSeconds = 31 },
		func(c *ReadinessCheck) { c.IntervalSeconds = 0 },
		func(c *ReadinessCheck) { c.TimeoutSeconds = 1; c.IntervalSeconds = 2 },
		func(c *ReadinessCheck) { c.ExpectedStatus = 199 },
		func(c *ReadinessCheck) { c.Port = 8123 },
	} {
		check := *runtimeFixture().Readiness
		change(&check)
		if check.Validate() == nil {
			t.Fatal("accepted readiness", check)
		}
	}
}

func TestEnvironmentStopDelayBounds(t *testing.T) {
	for _, path := range []string{"../secret", "/tmp/.env", ".", "", "a/../.env", "a//.env", "a\x00b", "a\nb"} {
		runtime := runtimeFixture()
		runtime.EnvironmentFiles = []string{path}
		if runtime.Validate() == nil {
			t.Fatal("accepted reference", path)
		}
	}
	for _, change := range []func(*CommandRuntime){
		func(r *CommandRuntime) { r.EnvironmentFiles = []string{".env", ".env"} },
		func(r *CommandRuntime) { r.EnvironmentFiles = make([]string, 17) },
		func(r *CommandRuntime) { r.DelaySeconds = -1 },
		func(r *CommandRuntime) { r.DelaySeconds = 3601 },
		func(r *CommandRuntime) { r.Stop.Signal = "SIGKILL" },
		func(r *CommandRuntime) { r.Stop.GraceSeconds = 0 },
		func(r *CommandRuntime) { r.Stop.GraceSeconds = 121 },
		func(r *CommandRuntime) { r.Stop.Action.Executable = "relative" },
	} {
		runtime := runtimeFixture()
		change(runtime)
		if runtime.Validate() == nil {
			t.Fatal("invalid runtime accepted")
		}
	}
}

func TestPreferencesApprovalAndSessionBranchCopies(t *testing.T) {
	preferences := &ProjectPreferences{PreviewURL: "http://localhost:8123/docs", Editor: &CommandAction{Executable: "/usr/bin/code", Arguments: []string{"/code/api", ""}, WorkingDirectory: "/code/api"}}
	preferences.Approval = &CommandApproval{ApprovedAt: testTime, Fingerprint: preferences.Fingerprint()}
	if err := preferences.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*ProjectPreferences){
		func(p *ProjectPreferences) { p.PreviewURL = "http://localhost:8123/other" },
		func(p *ProjectPreferences) { p.Editor.Arguments[0] = "other" },
		func(p *ProjectPreferences) { p.Editor.Executable = "/usr/bin/other" },
		func(p *ProjectPreferences) { p.Editor.WorkingDirectory = "/other" },
	} {
		copy := ClonePreferences(preferences)
		change(copy)
		if copy.Validate() == nil {
			t.Fatal("changed preferences retained approval")
		}
	}
	session := validSession()
	session.Projects[0].Preferences = preferences
	command := approvedCommand()
	command.Configuration.Runtime = runtimeFixture()
	command.Approval.Fingerprint = command.Configuration.Fingerprint()
	session.Projects[0].Commands = []StartupCommand{command}
	branch, err := session.Branch("Copy settings", testTime)
	if err != nil {
		t.Fatal(err)
	}
	branch.Projects[0].Commands[0].Configuration.Runtime.EnvironmentFiles[0] = "changed.env"
	branch.Projects[0].Commands[0].Configuration.Runtime.Stop.Action.Arguments[0] = "changed"
	branch.Projects[0].Commands[0].Configuration.Runtime.Readiness.TimeoutSeconds = 7
	branch.Projects[0].Preferences.Editor.Arguments[0] = "changed"
	branch.Projects[0].Preferences.Approval.Fingerprint = "changed"
	if err := session.Validate(); err != nil {
		t.Fatal("branch mutated source settings", err)
	}
	roundTrip(t, session)
}
