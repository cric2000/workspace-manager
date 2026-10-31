package storage

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"workspace-manager/internal/domain"
)

//go:embed testdata/settings-v1.json
var settingsV1 []byte

func codecFor[T Record](t *testing.T) *Codec[T] {
	t.Helper()
	codec, err := NewCodec[T](InitialSchemaVersion, nil)
	if err != nil {
		t.Fatal(err)
	}
	return codec
}

func projectFixture() domain.Project {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	return domain.Project{
		ID: "22222222222222222222222222222222", Name: "API", Path: "/missing/project with spaces",
		Source: domain.SourceImported, CreatedAt: now, UpdatedAt: now,
	}
}

func TestCodecRoundTripsAllRecordKinds(t *testing.T) {
	project := projectFixture()
	codecRoundTrip(t, "project", project)
	codecRoundTrip(t, "workspace", domain.Workspace{
		ID: "11111111111111111111111111111111", Name: "Coding", ProjectIDs: []domain.ID{project.ID},
		CreatedAt: project.CreatedAt, UpdatedAt: project.UpdatedAt,
	})
	codecRoundTrip(t, "session", domain.Session{
		ID: "33333333333333333333333333333333", Name: "Friday", CapturedAt: project.CreatedAt,
		Projects: []domain.Project{project}, CaptureWarnings: []string{"Editor buffers are not captured."},
	})
	codecRoundTrip(t, "settings", domain.DefaultSettings())
}

func codecRoundTrip[T Record](t *testing.T, kind string, value T) {
	t.Helper()
	codec := codecFor[T](t)
	data, err := codec.Encode(value)
	if err != nil {
		t.Fatal(err)
	}
	var document envelope
	if err := json.Unmarshal(data, &document); err != nil || document.Kind != kind || document.SchemaVersion != 1 {
		t.Fatalf("wrong envelope: %+v / %v", document, err)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatal("encoded documents should end with a newline")
	}
	restored, err := codec.Decode(data)
	if err != nil || !reflect.DeepEqual(value, restored) {
		t.Fatalf("round trip changed %T: %+v / %v", value, restored, err)
	}
}

func TestCodecRejectsAmbiguousCorruptAndUnsupportedDocuments(t *testing.T) {
	codec := codecFor[domain.Settings](t)
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"empty", nil, ErrInvalidDocument},
		{"truncated", settingsV1[:len(settingsV1)/2], ErrInvalidDocument},
		{"trailing value", append(bytes.Clone(settingsV1), []byte(` {}`)...), ErrInvalidDocument},
		{"unknown envelope field", bytes.Replace(settingsV1, []byte(`"kind":`), []byte(`"extra": 1, "kind":`), 1), ErrInvalidDocument},
		{"unknown payload field", bytes.Replace(settingsV1, []byte(`"appearance":`), []byte(`"extra": 1, "appearance":`), 1), ErrInvalidDocument},
		{"duplicate envelope key", bytes.Replace(settingsV1, []byte(`"schemaVersion": 1`), []byte(`"schemaVersion": 1, "schemaVersion": 2`), 1), ErrInvalidDocument},
		{"escaped duplicate", bytes.Replace(settingsV1, []byte(`"schemaVersion": 1`), []byte(`"schemaVersion": 1, "\u0073chemaVersion": 2`), 1), ErrInvalidDocument},
		{"case alias", bytes.Replace(settingsV1, []byte(`"appearance":`), []byte(`"Appearance": "dark", "appearance":`), 1), ErrInvalidDocument},
		{"Unicode fold alias", bytes.Replace(settingsV1, []byte(`"schemaVersion": 1`), []byte(`"schemaVersion": 1, "ſchemaVersion": 2`), 1), ErrInvalidDocument},
		{"invalid UTF-8", bytes.Replace(settingsV1, []byte("system"), []byte{'s', 0xff}, 1), ErrInvalidDocument},
		{"null payload", []byte(`{"schemaVersion":1,"kind":"settings","data":null}`), ErrInvalidDocument},
		{"missing payload", []byte(`{"schemaVersion":1,"kind":"settings"}`), ErrInvalidDocument},
		{"wrong scalar type", bytes.Replace(settingsV1, []byte(`"autostart": false`), []byte(`"autostart": "false"`), 1), ErrInvalidDocument},
		{"domain invalid", bytes.Replace(settingsV1, []byte("system"), []byte("unknown"), 1), ErrInvalidDocument},
		{"newer version", bytes.Replace(settingsV1, []byte(`"schemaVersion": 1`), []byte(`"schemaVersion": 2`), 1), ErrUnsupportedSchema},
		{"zero version", bytes.Replace(settingsV1, []byte(`"schemaVersion": 1`), []byte(`"schemaVersion": 0`), 1), ErrUnsupportedSchema},
		{"unversioned", []byte(`{"data":{}}`), ErrUnsupportedSchema},
		{"wrong record kind", bytes.Replace(settingsV1, []byte(`"settings"`), []byte(`"workspace"`), 1), ErrWrongKind},
		{"oversized", bytes.Repeat([]byte(" "), MaxDocumentBytes+1), ErrDocumentTooLarge},
		{"deep nesting", []byte(strings.Repeat("[", 70) + "0" + strings.Repeat("]", 70)), ErrInvalidDocument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := codec.Decode(tc.data)
			if !errors.Is(err, tc.want) || value != (domain.Settings{}) {
				t.Fatalf("expected zero settings and %v, got %+v / %v", tc.want, value, err)
			}
		})
	}
}

func TestCodecEncodeValidatesAndBoundsOutput(t *testing.T) {
	if _, err := codecFor[domain.Settings](t).Encode(domain.Settings{}); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("invalid settings encoded: %v", err)
	}
	session := domain.Session{
		ID: "33333333333333333333333333333333", Name: "Large capture",
		CapturedAt:      projectFixture().CreatedAt,
		CaptureWarnings: make([]string, 1100),
	}
	for index := range session.CaptureWarnings {
		session.CaptureWarnings[index] = strings.Repeat("a", 4096)
	}
	if _, err := codecFor[domain.Session](t).Encode(session); !errors.Is(err, ErrDocumentTooLarge) {
		t.Fatalf("oversized document encoded: %v", err)
	}
}

func TestMigrationsRunInOrderWithoutMutatingTheInput(t *testing.T) {
	before := bytes.Clone(settingsV1)
	var versions []int
	step := func(version int, key string, value any) Migration {
		return func(data json.RawMessage) (json.RawMessage, error) {
			versions = append(versions, version)
			var payload map[string]any
			if err := json.Unmarshal(data, &payload); err != nil {
				return nil, err
			}
			payload[key] = value
			return json.Marshal(payload)
		}
	}
	registry := map[int]Migration{1: step(1, "appearance", "light"), 2: step(2, "initialDelaySeconds", 15)}
	codec, err := NewCodec[domain.Settings](3, registry)
	if err != nil {
		t.Fatal(err)
	}
	delete(registry, 1) // Constructor must own its registration map.
	value, err := codec.Decode(settingsV1)
	if err != nil || value.Appearance != domain.AppearanceLight || value.InitialDelaySeconds != 15 {
		t.Fatalf("migration failed: %+v / %v", value, err)
	}
	if !reflect.DeepEqual(versions, []int{1, 2}) || !bytes.Equal(before, settingsV1) {
		t.Fatal("migration order or source immutability failed")
	}
	encoded, err := codec.Encode(value)
	if err != nil || !bytes.Contains(encoded, []byte(`"schemaVersion": 3`)) {
		t.Fatalf("new saves must use the configured current version: %s / %v", encoded, err)
	}
}

func TestMigrationFailuresDoNotReturnPartialValues(t *testing.T) {
	failure := errors.New("migration failed")
	for _, tc := range []struct {
		name    string
		version int
		steps   map[int]Migration
		want    error
	}{
		{"missing step", 2, nil, ErrUnsupportedSchema},
		{"gap", 3, map[int]Migration{1: func(data json.RawMessage) (json.RawMessage, error) { return data, nil }}, ErrUnsupportedSchema},
		{"failure", 2, map[int]Migration{1: func(json.RawMessage) (json.RawMessage, error) { return nil, failure }}, failure},
		{"corrupt output", 2, map[int]Migration{1: func(json.RawMessage) (json.RawMessage, error) { return []byte(`{`), nil }}, ErrInvalidDocument},
		{"invalid value", 2, map[int]Migration{1: func(json.RawMessage) (json.RawMessage, error) { return []byte(`{}`), nil }}, ErrInvalidDocument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codec, err := NewCodec[domain.Settings](tc.version, tc.steps)
			if err != nil {
				t.Fatal(err)
			}
			value, err := codec.Decode(settingsV1)
			if !errors.Is(err, tc.want) || value != (domain.Settings{}) {
				t.Fatalf("expected zero value and %v, got %+v / %v", tc.want, value, err)
			}
		})
	}
}

func TestCodecConfigurationMustBeExplicit(t *testing.T) {
	for _, tc := range []struct {
		version int
		steps   map[int]Migration
	}{
		{0, nil}, {-1, nil}, {1, map[int]Migration{1: func(data json.RawMessage) (json.RawMessage, error) { return data, nil }}},
		{2, map[int]Migration{0: func(data json.RawMessage) (json.RawMessage, error) { return data, nil }}},
		{2, map[int]Migration{1: nil}},
	} {
		if _, err := NewCodec[domain.Settings](tc.version, tc.steps); err == nil {
			t.Fatal("accepted invalid codec configuration")
		}
	}
	var codec Codec[domain.Settings]
	if _, err := codec.Encode(domain.DefaultSettings()); err == nil {
		t.Fatal("zero codec encoded a document")
	}
	if _, err := codec.Decode(settingsV1); err == nil {
		t.Fatal("zero codec decoded a document")
	}
}
