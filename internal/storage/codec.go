package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
	"unicode"
	"unicode/utf8"

	"workspace-manager/internal/domain"
)

const (
	InitialSchemaVersion = 1
	MaxDocumentBytes     = 4 << 20
)

var (
	ErrInvalidDocument   = errors.New("invalid storage document")
	ErrUnsupportedSchema = errors.New("unsupported storage schema")
	ErrWrongKind         = errors.New("unexpected storage record kind")
	ErrDocumentTooLarge  = errors.New("storage document exceeds size limit")
)

// Record limits the codec to persistent domain values; runtime processes and
// arbitrary environment maps cannot accidentally become supported record types.
type Record interface {
	domain.Workspace | domain.Project | domain.Session | domain.Settings
	Validate() error
}

// Migration transforms only the data payload from version N to N+1. Migrations
// must be pure: Decode never writes upgraded data back to disk automatically.
type Migration func(json.RawMessage) (json.RawMessage, error)

type envelope struct {
	SchemaVersion int             `json:"schemaVersion"`
	Kind          string          `json:"kind"`
	Data          json.RawMessage `json:"data"`
}

// Codec is immutable after construction and can be shared between goroutines.
// Each record kind selects its schema; production has no legacy/version-zero format.
type Codec[T Record] struct {
	version    int
	migrations map[int]Migration
}

func NewCodec[T Record](version int, migrations map[int]Migration) (*Codec[T], error) {
	if version < 1 {
		return nil, errors.New("schema version must be positive")
	}
	for from, migrate := range migrations {
		if from < 1 || from >= version || migrate == nil {
			return nil, errors.New("migration must upgrade a positive older version by one step")
		}
	}
	return &Codec[T]{version: version, migrations: maps.Clone(migrations)}, nil
}

func (codec *Codec[T]) Encode(value T) ([]byte, error) {
	if codec == nil || codec.version < 1 {
		return nil, errors.New("codec must be constructed with NewCodec")
	}
	if err := value.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidDocument, err)
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: payload cannot be encoded", ErrInvalidDocument)
	}
	data, err := json.MarshalIndent(envelope{codec.version, kindOf[T](), payload}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("%w: envelope cannot be encoded", ErrInvalidDocument)
	}
	data = append(data, '\n')
	if len(data) > MaxDocumentBytes {
		return nil, ErrDocumentTooLarge
	}
	return data, nil
}

func (codec *Codec[T]) Decode(data []byte) (T, error) {
	var zero T
	if codec == nil || codec.version < 1 {
		return zero, errors.New("codec must be constructed with NewCodec")
	}
	if err := checkJSON(data); err != nil {
		return zero, err
	}
	var document envelope
	if err := decodeFields(data, &document); err != nil {
		return zero, err
	}
	if document.SchemaVersion < 1 || document.SchemaVersion > codec.version {
		return zero, ErrUnsupportedSchema
	}
	if document.Kind != kindOf[T]() {
		return zero, ErrWrongKind
	}
	payload := document.Data
	for version := document.SchemaVersion; version < codec.version; version++ {
		migrate := codec.migrations[version]
		if migrate == nil {
			return zero, fmt.Errorf("%w: no migration from version %d", ErrUnsupportedSchema, version)
		}
		var err error
		// Give each migration its own buffer; it cannot mutate the input document.
		payload, err = migrate(bytes.Clone(payload))
		if err != nil {
			return zero, fmt.Errorf("migration from version %d: %w", version, err)
		}
		if err := checkJSON(payload); err != nil {
			return zero, fmt.Errorf("migration from version %d: %w", version, err)
		}
	}
	if len(payload) == 0 || bytes.Equal(bytes.TrimSpace(payload), []byte("null")) {
		return zero, ErrInvalidDocument
	}
	var value T
	if err := decodeFields(payload, &value); err != nil {
		return zero, err
	}
	if err := value.Validate(); err != nil {
		return zero, fmt.Errorf("%w: %w", ErrInvalidDocument, err)
	}
	return value, nil
}

func kindOf[T Record]() string {
	var value T
	switch any(value).(type) {
	case domain.Workspace:
		return "workspace"
	case domain.Project:
		return "project"
	case domain.Session:
		return "session"
	case domain.Settings:
		return "settings"
	default:
		panic("Record constraint and kindOf must be updated together")
	}
}

func decodeFields(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		// Parser errors may quote untrusted field values; don't propagate them.
		return ErrInvalidDocument
	}
	return nil
}

// encoding/json accepts duplicate keys by default. Reject them, including casing
// aliases accepted by its struct decoder. Preserve its normal scalar/time rules.
func checkJSON(data []byte) error {
	if len(data) > MaxDocumentBytes {
		return ErrDocumentTooLarge
	}
	if !utf8.Valid(data) {
		return ErrInvalidDocument
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkJSON(decoder, 0); err != nil {
		return ErrInvalidDocument
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalidDocument
	}
	return nil
}

func walkJSON(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return ErrInvalidDocument
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[foldJSONKey(name)] {
				return ErrInvalidDocument
			}
			seen[foldJSONKey(name)] = true
			if err := walkJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := walkJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return ErrInvalidDocument
	}
	_, err = decoder.Token() // Decoder verifies the matching closing delimiter.
	return err
}

func foldJSONKey(key string) string {
	// Match encoding/json's Unicode simple-fold field aliases, including long S.
	return strings.Map(func(character rune) rune {
		minimum := character
		for next := unicode.SimpleFold(character); next != character; next = unicode.SimpleFold(next) {
			if next < minimum {
				minimum = next
			}
		}
		return minimum
	}, key)
}
