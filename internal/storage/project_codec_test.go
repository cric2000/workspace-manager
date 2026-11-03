package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"workspace-manager/internal/domain"
)

func TestProjectMigrationAndExactHistory(t *testing.T) {
	value := projectFixture()
	legacy, err := codecFor[domain.Project](t).Encode(value)
	if err != nil {
		t.Fatal(err)
	}
	codec, _ := NewProjectCodec()
	path := filepath.Join(t.TempDir(), "projects")
	repo, err := OpenRepository(path, codec)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	filename := filepath.Join(path, string(value.ID)+".json")
	if err := os.WriteFile(filename, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := repo.Get(context.Background(), value.ID)
	if err != nil || len(before.Value.Commands) != 0 {
		t.Fatal(before, err)
	}
	untouched, _ := os.ReadFile(filename)
	if !bytes.Equal(untouched, legacy) {
		t.Fatal("read migrated bytes on disk")
	}
	if err := repo.Update(context.Background(), value.ID, before.Revision, func(p domain.Project) (domain.Project, error) { p.Name = "Explicit edit"; return p, nil }); err != nil {
		t.Fatal(err)
	}
	current, _ := os.ReadFile(filename)
	if !bytes.Contains(current, []byte(`"schemaVersion": 3`)) {
		t.Fatal("did not save schema 3")
	}
	archive, err := os.ReadFile(filepath.Join(path, "history", string(value.ID), string(before.Revision)+".json"))
	if err != nil || !bytes.Equal(archive, legacy) {
		t.Fatal("old bytes not archived", err)
	}
	for _, field := range []string{`"commands":[],`, `"future":true,`} {
		bad := bytes.Replace(legacy, []byte(`"name":`), []byte(field+`"name":`), 1)
		if _, err := codec.Decode(bad); !errors.Is(err, ErrInvalidDocument) {
			t.Fatal("legacy field smuggled", err)
		}
	}
}

func approvedProjectV2(t *testing.T) domain.Project {
	t.Helper()
	project := projectFixture()
	// Independent legacy payload/hash: no runtime field existed in schema 2.
	configuration := struct {
		Name             string   `json:"name"`
		Executable       string   `json:"executable"`
		Arguments        []string `json:"arguments"`
		WorkingDirectory string   `json:"workingDirectory"`
	}{"API", "/usr/bin/go", []string{"run", ".", ""}, project.Path}
	digest := sha256.Sum256(mustJSON(t, configuration))
	project.Commands = []domain.StartupCommand{{ID: "11111111111111111111111111111111", Configuration: domain.CommandConfiguration{Name: configuration.Name, Executable: configuration.Executable, Arguments: configuration.Arguments, WorkingDirectory: configuration.WorkingDirectory}, Approval: &domain.CommandApproval{ApprovedAt: project.CreatedAt, Fingerprint: hex.EncodeToString(digest[:])}}}
	return project
}

func TestV2MigrationPreservesApprovalAndRejectsNewSettings(t *testing.T) {
	project := approvedProjectV2(t)
	oldCodec, _ := NewCodec[domain.Project](2, nil)
	legacy, err := oldCodec.Encode(project)
	if err != nil {
		t.Fatal(err)
	}
	codec, _ := NewProjectCodec()
	value, err := codec.Decode(legacy)
	if err != nil || !reflect.DeepEqual(value, project) || value.Commands[0].Configuration.Runtime != nil || value.Preferences != nil {
		t.Fatal(value, err)
	}
	for _, bad := range [][]byte{
		bytes.Replace(legacy, []byte(`"commands":`), []byte(`"preferences":{},"commands":`), 1),
		bytes.Replace(legacy, []byte(`"workingDirectory":`), []byte(`"runtime":{},"workingDirectory":`), 1),
	} {
		if _, err := codec.Decode(bad); !errors.Is(err, ErrInvalidDocument) {
			t.Fatal("settings smuggled into schema 2", err)
		}
	}
	path := filepath.Join(t.TempDir(), "projects")
	repo, err := OpenRepository(path, codec)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	filename := filepath.Join(path, string(project.ID)+".json")
	if err := os.WriteFile(filename, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := repo.Get(context.Background(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, _ := os.ReadFile(filename)
	if !bytes.Equal(unchanged, legacy) {
		t.Fatal("schema 2 rewritten on read")
	}
	if err := repo.Update(context.Background(), project.ID, before.Revision, func(p domain.Project) (domain.Project, error) { p.Name = "Explicit edit"; return p, nil }); err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(filepath.Join(path, "history", string(project.ID), string(before.Revision)+".json"))
	if err != nil || !bytes.Equal(archive, legacy) {
		t.Fatal("schema 2 bytes not archived", err)
	}
	session := sessionFixture()
	session.Projects = []domain.Project{project}
	oldSessionCodec, _ := NewCodec[domain.Session](2, nil)
	legacySession, err := oldSessionCodec.Encode(session)
	if err != nil {
		t.Fatal(err)
	}
	sessionCodec, _ := NewSessionCodec()
	loaded, err := sessionCodec.Decode(legacySession)
	if err != nil || !reflect.DeepEqual(loaded, session) {
		t.Fatal(loaded, err)
	}
	bad := bytes.Replace(legacySession, []byte(`"workingDirectory":`), []byte(`"runtime":{},"workingDirectory":`), 1)
	if _, err := sessionCodec.Decode(bad); !errors.Is(err, ErrInvalidDocument) {
		t.Fatal("nested new settings accepted in schema 2", err)
	}
}

func TestSessionMigrationStrictlyChecksEmbeddedProjects(t *testing.T) {
	value := sessionFixture()
	legacy, err := codecFor[domain.Session](t).Encode(value)
	if err != nil {
		t.Fatal(err)
	}
	codec, _ := NewSessionCodec()
	current, err := codec.Decode(legacy)
	if err != nil || len(current.Projects) != len(value.Projects) || len(current.Projects[0].Commands) != 0 {
		t.Fatal(current, err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(legacy, &envelope); err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	_ = json.Unmarshal(envelope["data"], &payload)
	var projects []map[string]json.RawMessage
	_ = json.Unmarshal(payload["projects"], &projects)
	projects[0]["commands"] = json.RawMessage(`[]`)
	payload["projects"] = mustJSON(t, projects)
	envelope["data"] = mustJSON(t, payload)
	if _, err := codec.Decode(mustJSON(t, envelope)); !errors.Is(err, ErrInvalidDocument) {
		t.Fatal("nested legacy commands accepted", err)
	}
	future := bytes.Replace(legacy, []byte(`"schemaVersion": 1`), []byte(`"schemaVersion": 5`), 1)
	if _, err := codec.Decode(future); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatal(err)
	}
}
