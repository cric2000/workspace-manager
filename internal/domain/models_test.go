package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	workspaceID ID = "11111111111111111111111111111111"
	projectID   ID = "22222222222222222222222222222222"
	sessionID   ID = "33333333333333333333333333333333"
)

var testTime = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func validWorkspace() Workspace {
	return Workspace{
		ID: workspaceID, Name: "Coding", Description: "Local development",
		ProjectIDs: []ID{projectID}, CreatedAt: testTime, UpdatedAt: testTime,
	}
}

func validProject() Project {
	return Project{
		ID: projectID, Name: "API", Path: "/not-required-to-exist/project with spaces",
		Source: SourceImported, CreatedAt: testTime, UpdatedAt: testTime,
	}
}

func validSession() Session {
	return Session{
		ID: sessionID, Name: "Friday coding", CapturedAt: testTime,
		WorkspaceID: workspaceID, Projects: []Project{validProject()},
		CaptureWarnings: []string{"Unsaved editor buffers are not captured."},
	}
}

func TestNewIDAndPathComponentValidation(t *testing.T) {
	seen := map[ID]bool{}
	for range 100 {
		id, err := NewID()
		if err != nil {
			t.Fatal(err)
		}
		if err := id.Validate(); err != nil {
			t.Fatal(err)
		}
		if seen[id] {
			t.Fatal("generated a repeated ID")
		}
		seen[id] = true
	}
	for _, id := range []ID{"", "../settings", "/absolute", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "gggggggggggggggggggggggggggggggg", "a000000000000000000000000000000"} {
		if err := id.Validate(); err == nil {
			t.Errorf("accepted invalid ID %q", id)
		}
	}
	if err := (ID("0123456789abcdef0123456789abcdef")).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceValidation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Workspace)
		field  string
	}{
		{"missing ID", func(w *Workspace) { w.ID = "" }, "id"},
		{"empty name", func(w *Workspace) { w.Name = "" }, "name"},
		{"space padded name", func(w *Workspace) { w.Name = " Coding " }, "name"},
		{"control in name", func(w *Workspace) { w.Name = "Coding\n" }, "name"},
		{"invalid UTF-8", func(w *Workspace) { w.Name = "\xff" }, "name"},
		{"long name", func(w *Workspace) { w.Name = strings.Repeat("界", 121) }, "name"},
		{"long description", func(w *Workspace) { w.Description = strings.Repeat("a", 4097) }, "description"},
		{"NUL description", func(w *Workspace) { w.Description = "a\x00b" }, "description"},
		{"zero created timestamp", func(w *Workspace) { w.CreatedAt = time.Time{} }, "createdAt"},
		{"zero updated timestamp", func(w *Workspace) { w.UpdatedAt = time.Time{} }, "updatedAt"},
		{"time order", func(w *Workspace) { w.UpdatedAt = w.CreatedAt.Add(-time.Second) }, "updatedAt"},
		{"invalid project ID", func(w *Workspace) { w.ProjectIDs = []ID{"../../other"} }, "projectIds[0]"},
		{"duplicate projects", func(w *Workspace) { w.ProjectIDs = []ID{projectID, projectID} }, "projectIds[1]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := validWorkspace()
			tc.change(&workspace)
			if err := workspace.Validate(); err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("expected %s validation error, got %v", tc.field, err)
			}
		})
	}
	workspace := validWorkspace()
	workspace.Name = strings.Repeat("界", 120)
	workspace.ProjectIDs = nil // An empty profile is valid.
	if err := workspace.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestProjectValidationDoesNotRequireDirectoryAvailability(t *testing.T) {
	if err := validProject().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", "relative", "~/code", "/code/../project", "/code\x00/project", "/code/\xff"} {
		project := validProject()
		project.Path = path
		if err := project.Validate(); err == nil {
			t.Errorf("accepted invalid path %q", path)
		}
	}
	project := validProject()
	project.Source = "unknown"
	if err := project.Validate(); err == nil {
		t.Fatal("accepted unknown project source")
	}
	project.Source = SourceBootstrapped
	if err := project.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Project)
	}{
		{"invalid identity", func(p *Project) { p.ID = "bad" }},
		{"invalid name", func(p *Project) { p.Name = "" }},
		{"invalid timestamps", func(p *Project) { p.UpdatedAt = p.CreatedAt.Add(-time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := validProject()
			tc.change(&project)
			if err := project.Validate(); err == nil {
				t.Fatal("accepted an invalid project record")
			}
		})
	}
}

func TestRecordsRoundTripThroughJSON(t *testing.T) {
	roundTrip(t, validWorkspace())
	roundTrip(t, validProject())
	roundTrip(t, validSession())
	roundTrip(t, DefaultSettings())
}

func roundTrip[T interface{ Validate() error }](t *testing.T, value T) {
	t.Helper()
	if err := value.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var restored T
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.Validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(value, restored) {
		t.Fatalf("JSON round trip changed %T", value)
	}
}

func TestSessionBranchesAreIndependentAndRetainLineage(t *testing.T) {
	original := validSession()
	before, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := original.Branch("Friday experiment", testTime.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if branch.ID == original.ID || branch.ParentSessionID != original.ID || branch.WorkspaceID != original.WorkspaceID {
		t.Fatal("branch identity or lineage is incorrect")
	}
	if branch.Projects[0].ID != original.Projects[0].ID || branch.Projects[0].Path != original.Projects[0].Path {
		t.Fatal("branch duplicated project identity or changed its physical path")
	}
	branch.Projects[0].Name = "Changed only in branch"
	branch.CaptureWarnings[0] = "Branch-specific warning"
	child, err := branch.Branch("Another experiment", testTime.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentSessionID != branch.ID || child.ID == original.ID || child.ID == branch.ID {
		t.Fatal("multiple generations must have independent identities and immediate parents")
	}
	after, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("branching or editing the branch changed the original snapshot")
	}
	roundTrip(t, child)
}

func TestSessionRejectsInvalidSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Session)
	}{
		{"bad identity", func(s *Session) { s.ID = "bad" }},
		{"bad name", func(s *Session) { s.Name = "" }},
		{"self parent", func(s *Session) { s.ParentSessionID = s.ID }},
		{"bad parent", func(s *Session) { s.ParentSessionID = "bad" }},
		{"bad workspace", func(s *Session) { s.WorkspaceID = "bad" }},
		{"zero capture time", func(s *Session) { s.CapturedAt = time.Time{} }},
		{"JSON-incompatible time", func(s *Session) { s.CapturedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
		{"bad project", func(s *Session) { s.Projects[0].Path = "relative" }},
		{"duplicate snapshots", func(s *Session) { s.Projects = append(s.Projects, s.Projects[0]) }},
		{"invalid warning", func(s *Session) { s.CaptureWarnings = []string{"\x00"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := validSession()
			tc.change(&session)
			if err := session.Validate(); err == nil {
				t.Fatal("accepted invalid snapshot")
			}
			if _, err := session.Branch("Branch", testTime); err == nil {
				t.Fatal("branched an invalid snapshot")
			}
		})
	}
	session := validSession()
	session.WorkspaceID = "" // Standalone captures need no workspace association.
	if err := session.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", " Bad name ", "Bad\nname"} {
		if _, err := session.Branch(name, testTime); err == nil {
			t.Errorf("accepted invalid branch name %q", name)
		}
	}
	if _, err := session.Branch("Branch", time.Time{}); err == nil {
		t.Fatal("accepted a zero branch timestamp")
	}
	if _, err := session.Branch("Branch", time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("accepted a JSON-incompatible branch timestamp")
	}
}

func TestSettingsAutostartRequirements(t *testing.T) {
	if err := DefaultSettings().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []StartupMode{StartupFixed, StartupRestore, StartupHybrid} {
		t.Run(string(mode), func(t *testing.T) {
			settings := DefaultSettings()
			settings.StartupMode = mode
			settings.Autostart = true
			if err := settings.Validate(); err == nil {
				t.Fatal("enabled autostart without required selections")
			}
			if mode != StartupRestore {
				settings.DefaultWorkspaceID = workspaceID
			}
			if mode == StartupHybrid && settings.Validate() == nil {
				t.Fatal("hybrid autostart accepted a missing restore session")
			}
			if mode != StartupFixed {
				settings.RestoreSessionID = sessionID
			}
			if err := settings.Validate(); err != nil {
				t.Fatal(err)
			}
			if mode == StartupHybrid {
				settings.DefaultWorkspaceID = ""
				if err := settings.Validate(); err == nil {
					t.Fatal("hybrid autostart accepted a missing default workspace")
				}
			}
		})
	}
	for _, mutate := range []func(*Settings){
		func(s *Settings) { s.StartupMode = "unknown" },
		func(s *Settings) { s.Appearance = "unknown" },
		func(s *Settings) { s.DefaultWorkspaceID = "bad" },
		func(s *Settings) { s.RestoreSessionID = "bad" },
		func(s *Settings) { s.InitialDelaySeconds = -1 },
		func(s *Settings) { s.InitialDelaySeconds = 3601 },
	} {
		settings := DefaultSettings()
		mutate(&settings)
		if err := settings.Validate(); err == nil {
			t.Fatal("accepted invalid settings")
		}
	}
}
