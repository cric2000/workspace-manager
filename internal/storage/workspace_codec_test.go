package storage

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"workspace-manager/internal/domain"
)

//go:embed testdata/workspace-v1.json
var workspaceV1 []byte

func TestWorkspaceUpgradePreservesExactOldBytesUntilExplicitSave(t *testing.T) {
	codec, err := NewWorkspaceCodec()
	if err != nil {
		t.Fatal(err)
	}
	value, err := codec.Decode(workspaceV1)
	if err != nil || value.Name != "Existing Coding" || len(value.ProjectIDs) != 1 || value.Applications == nil || len(value.Applications) != 0 {
		t.Fatal(value, err)
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "profiles")
	repository, err := OpenRepository(path, codec)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	filename := filepath.Join(path, string(value.ID)+".json")
	if err := os.WriteFile(filename, workspaceV1, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := repository.Get(ctx, value.ID)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, _ := os.ReadFile(filename)
	if !bytes.Equal(unchanged, workspaceV1) {
		t.Fatal("read silently rewrote schema 1")
	}
	choice := domain.WorkspaceApplication{DesktopID: "org.example.Editor.desktop", DuplicatePolicy: domain.SkipIfRunning}
	if err := repository.Update(ctx, value.ID, before.Revision, func(value domain.Workspace) (domain.Workspace, error) {
		value.Applications = []domain.WorkspaceApplication{choice}
		return value, nil
	}); err != nil {
		t.Fatal(err)
	}
	current, _ := os.ReadFile(filename)
	if !bytes.Contains(current, []byte(`"schemaVersion": 4`)) {
		t.Fatal("save did not write workspace schema 4")
	}
	archived, err := os.ReadFile(filepath.Join(path, "history", string(value.ID), string(before.Revision)+".json"))
	if err != nil || !bytes.Equal(archived, workspaceV1) {
		t.Fatal("legacy bytes not preserved in history", err)
	}
	after, err := repository.Get(ctx, value.ID)
	if err != nil || after.Value.Applications[0] != choice || after.Value.ProjectIDs[0] != value.ProjectIDs[0] || after.Value.ID != value.ID || !after.Value.CreatedAt.Equal(value.CreatedAt) {
		t.Fatal(after, err)
	}
	if err := repository.Update(ctx, value.ID, before.Revision, func(value domain.Workspace) (domain.Workspace, error) {
		t.Fatal("stale edit callback ran")
		return value, nil
	}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

func TestWorkspaceV2MigrationPreservesDesktopChoicesAndArchivesBytes(t *testing.T) {
	codec, _ := NewWorkspaceCodec()
	value, err := codec.Decode(workspaceV1)
	if err != nil {
		t.Fatal(err)
	}
	value.Applications = []domain.WorkspaceApplication{{DesktopID: "org.example.Editor.desktop", DuplicatePolicy: domain.SkipIfRunning}}
	data, err := json.MarshalIndent(envelope{SchemaVersion: 2, Kind: "workspace", Data: mustJSON(t, value)}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "profiles")
	repo, err := OpenRepository(path, codec)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	filename := filepath.Join(path, string(value.ID)+".json")
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := repo.Get(ctx, value.ID)
	if err != nil || before.Value.Applications[0].DesktopID != value.Applications[0].DesktopID {
		t.Fatal(before, err)
	}
	untouched, _ := os.ReadFile(filename)
	if !bytes.Equal(data, untouched) {
		t.Fatal("read rewrote schema 2")
	}
	if err := repo.Update(ctx, value.ID, before.Revision, func(w domain.Workspace) (domain.Workspace, error) { w.Description = "explicit save"; return w, nil }); err != nil {
		t.Fatal(err)
	}
	archived, err := os.ReadFile(filepath.Join(path, "history", string(value.ID), string(before.Revision)+".json"))
	if err != nil || !bytes.Equal(data, archived) {
		t.Fatal("schema 2 history lost", err)
	}
	// Never accept custom executable fields smuggled into a schema-2 document.
	smuggled := bytes.Replace(data, []byte(`"duplicatePolicy":`), []byte(`"custom": {}, "duplicatePolicy":`), 1)
	if _, err := codec.Decode(smuggled); !errors.Is(err, ErrInvalidDocument) {
		t.Fatal("unknown old custom field accepted", err)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestWorkspaceMigrationRefusesUnknownFieldsAndNewerSchema(t *testing.T) {
	codec, _ := NewWorkspaceCodec()
	for _, document := range [][]byte{
		bytes.Replace(workspaceV1, []byte(`"name":`), []byte(`"future": true, "name":`), 1),
		bytes.Replace(workspaceV1, []byte(`"name":`), []byte(`"applications": [], "name":`), 1),
		bytes.Replace(workspaceV1, []byte(`"name":`), []byte(`"name": "duplicate", "name":`), 1),
	} {
		if _, err := codec.Decode(document); !errors.Is(err, ErrInvalidDocument) {
			t.Fatal("unsafe legacy payload migrated", err)
		}
	}
	future := bytes.Replace(workspaceV1, []byte(`"schemaVersion": 1`), []byte(`"schemaVersion": 5`), 1)
	if _, err := codec.Decode(future); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatal(err)
	}
	// Settings remain at schema 1; workspace versioning is independent.
	settings, err := codecFor[domain.Settings](t).Decode(settingsV1)
	if err != nil || settings.Validate() != nil {
		t.Fatal(settings, err)
	}
}
