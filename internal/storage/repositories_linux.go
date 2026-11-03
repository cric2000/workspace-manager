package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"workspace-manager/internal/domain"
)

// Entity records have stable IDs; settings are a separate singleton repository.
type Entity interface {
	Record
	domain.Workspace | domain.Project | domain.Session | domain.SetupLog | domain.CreationLog | domain.BootstrapRecovery | domain.ManagedRun | domain.StartupAuthorization | domain.ManagedSelection
}

type Repository[T Entity] struct{ store *recordStore[T] }

func OpenRepository[T Entity](path string, codec *Codec[T]) (*Repository[T], error) {
	store, err := openRecordStore(path, codec, func(name string, value T) error {
		if string(entityID(value))+".json" != name {
			return fmt.Errorf("%w: record ID does not match its filename", ErrInvalidDocument)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Repository[T]{store: store}, nil
}

func (repository *Repository[T]) Close() error { return repository.store.close() }

func entityID[T Entity](value T) domain.ID {
	switch entity := any(value).(type) {
	case domain.Workspace:
		return entity.ID
	case domain.Project:
		return entity.ID
	case domain.Session:
		return entity.ID
	case domain.SetupLog:
		return entity.ID
	case domain.CreationLog:
		return entity.ID
	case domain.BootstrapRecovery:
		return entity.ID
	case domain.ManagedRun:
		return entity.ID
	case domain.StartupAuthorization:
		return entity.ID
	case domain.ManagedSelection:
		return entity.ID
	default:
		panic("Entity constraint and entityID must be updated together")
	}
}

func entityFilename(id domain.ID) (string, error) {
	if err := id.Validate(); err != nil {
		return "", err
	}
	return string(id) + ".json", nil
}

func (repository *Repository[T]) Create(ctx context.Context, value T) error {
	name, err := entityFilename(entityID(value))
	if err != nil {
		return err
	}
	return repository.store.create(ctx, name, value, func() error {
		if session, ok := any(value).(domain.Session); ok {
			visited := map[domain.ID]bool{session.ID: true}
			parent := session.ParentSessionID
			for parent != "" {
				if err := ctx.Err(); err != nil {
					return err
				}
				if visited[parent] || len(visited) > 1024 {
					return fmt.Errorf("%w: cyclic or excessive session ancestry", ErrInvalidDocument)
				}
				visited[parent] = true
				snapshot, _, err := repository.store.read(string(parent) + ".json")
				if err != nil {
					return fmt.Errorf("parent session: %w", err)
				}
				parent = any(snapshot.Value).(domain.Session).ParentSessionID
			}
		}
		return nil
	})
}

func (repository *Repository[T]) Get(ctx context.Context, id domain.ID) (Snapshot[T], error) {
	name, err := entityFilename(id)
	if err != nil {
		return Snapshot[T]{}, err
	}
	return repository.store.get(ctx, name)
}

func (repository *Repository[T]) List(ctx context.Context) ([]Snapshot[T], error) {
	return repository.ListBounded(ctx, 0)
}

var ErrRecordLimit = errors.New("repository record count exceeds the requested limit")

// ListBounded fails without partial results when limit is exceeded. Zero keeps
// the ordinary List behavior. Directory entries are read in bounded batches.
func (repository *Repository[T]) ListBounded(ctx context.Context, limit int) ([]Snapshot[T], error) {
	if limit < 0 {
		return nil, ErrRecordLimit
	}
	var snapshots []Snapshot[T]
	err := repository.Scan(ctx, func(snapshot Snapshot[T]) error {
		if limit > 0 && len(snapshots) >= limit {
			return ErrRecordLimit
		}
		snapshots = append(snapshots, snapshot)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(snapshots, func(i, j int) bool { return entityID(snapshots[i].Value) < entityID(snapshots[j].Value) })
	return snapshots, nil
}

// Scan visits one bounded document at a time under the repository lock. The
// callback must be quick and must not call back into this repository. Callers
// can retain a bounded selection without first loading all record bodies.
func (repository *Repository[T]) Scan(ctx context.Context, visit func(Snapshot[T]) error) error {
	if visit == nil {
		return ErrInvalidDocument
	}
	return repository.store.transaction(ctx, func() error {
		directory, err := privateDirectory(repository.store.root)
		if err != nil {
			return err
		}
		defer directory.Close()
		for {
			entries, err := directory.ReadDir(128)
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			if len(entries) == 0 {
				return ctx.Err()
			}
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				if !strings.HasSuffix(entry.Name(), ".json") {
					continue
				}
				id := domain.ID(strings.TrimSuffix(entry.Name(), ".json"))
				if err := id.Validate(); err != nil {
					return fmt.Errorf("%w: unexpected JSON filename in repository", ErrInvalidDocument)
				}
				snapshot, _, err := repository.store.read(entry.Name())
				if err != nil {
					return err
				}
				if err := visit(snapshot); err != nil {
					return err
				}
			}
		}
	})
}

// Update rejects sessions. Changes run once while the transaction lock is held;
// callbacks must be quick and must not call back into the same repository.
func (repository *Repository[T]) Update(ctx context.Context, id domain.ID, expected Revision, change func(T) (T, error)) error {
	var value T
	if _, session := any(value).(domain.Session); session {
		return ErrImmutable
	}
	name, err := entityFilename(id)
	if err != nil {
		return err
	}
	return repository.store.update(ctx, name, expected, change)
}

// ReviseSession is intentionally distinct from ordinary entity updates. The UI
// must use it only for an explicitly confirmed change; old bytes are backed up.
func ReviseSession(ctx context.Context, repository *Repository[domain.Session], id domain.ID, expected Revision, change func(domain.Session) (domain.Session, error)) error {
	name, err := entityFilename(id)
	if err != nil {
		return err
	}
	return repository.store.update(ctx, name, expected, change)
}

func (repository *Repository[T]) Delete(ctx context.Context, id domain.ID, expected Revision) error {
	name, err := entityFilename(id)
	if err != nil {
		return err
	}
	return repository.store.remove(ctx, name, expected)
}

func (repository *Repository[T]) History(ctx context.Context, id domain.ID) ([]Revision, error) {
	name, err := entityFilename(id)
	if err != nil {
		return nil, err
	}
	return repository.store.historyFor(ctx, name)
}

func (repository *Repository[T]) GetRevision(ctx context.Context, id domain.ID, revision Revision) (Snapshot[T], error) {
	name, err := entityFilename(id)
	if err != nil {
		return Snapshot[T]{}, err
	}
	return repository.store.getRevision(ctx, name, revision)
}

// RestoreRevision is an explicit history restore, including corruption recovery
// or undelete (expected=""). It refuses unknown newer schemas and stale tokens.
func (repository *Repository[T]) RestoreRevision(ctx context.Context, id domain.ID, expected, revision Revision) error {
	name, err := entityFilename(id)
	if err != nil {
		return err
	}
	return repository.store.restore(ctx, name, expected, revision)
}

type SettingsRepository struct{ store *recordStore[domain.Settings] }

func OpenSettingsRepository(path string, codec *Codec[domain.Settings]) (*SettingsRepository, error) {
	store, err := openRecordStore(path, codec, func(string, domain.Settings) error { return nil })
	if err != nil {
		return nil, err
	}
	return &SettingsRepository{store: store}, nil
}

func (repository *SettingsRepository) Close() error { return repository.store.close() }

func (repository *SettingsRepository) Get(ctx context.Context) (Snapshot[domain.Settings], error) {
	return repository.store.get(ctx, "settings.json")
}

func (repository *SettingsRepository) Create(ctx context.Context, value domain.Settings) error {
	return repository.store.create(ctx, "settings.json", value, nil)
}

func (repository *SettingsRepository) Update(ctx context.Context, expected Revision, change func(domain.Settings) (domain.Settings, error)) error {
	return repository.store.update(ctx, "settings.json", expected, change)
}

func (repository *SettingsRepository) History(ctx context.Context) ([]Revision, error) {
	return repository.store.historyFor(ctx, "settings.json")
}

func (repository *SettingsRepository) GetRevision(ctx context.Context, revision Revision) (Snapshot[domain.Settings], error) {
	return repository.store.getRevision(ctx, "settings.json", revision)
}

func (repository *SettingsRepository) RestoreRevision(ctx context.Context, expected, revision Revision) error {
	return repository.store.restore(ctx, "settings.json", expected, revision)
}

func stableIdentity[T Record](before, after T) error {
	valid := true
	switch old := any(before).(type) {
	case domain.Workspace:
		next := any(after).(domain.Workspace)
		valid = old.ID == next.ID && old.CreatedAt.Equal(next.CreatedAt) && !next.UpdatedAt.Before(old.UpdatedAt)
	case domain.Project:
		next := any(after).(domain.Project)
		valid = old.ID == next.ID && old.Source == next.Source && old.CreatedAt.Equal(next.CreatedAt) && !next.UpdatedAt.Before(old.UpdatedAt)
	case domain.Session:
		next := any(after).(domain.Session)
		valid = old.ID == next.ID && old.ParentSessionID == next.ParentSessionID
	case domain.ManagedRun:
		next := any(after).(domain.ManagedRun)
		valid = old.ID == next.ID && old.ProjectID == next.ProjectID && old.Command.Configuration.Fingerprint() == next.Command.Configuration.Fingerprint() && old.StartedAt.Equal(next.StartedAt) && !next.UpdatedAt.Before(old.UpdatedAt)
	case domain.SetupLog:
		next := any(after).(domain.SetupLog)
		valid = old.ID == next.ID && old.ProjectID == next.ProjectID && old.Kind == next.Kind && old.Label == next.Label && old.RecordedAt.Equal(next.RecordedAt) && old.Result == nil && next.Result != nil
	case domain.CreationLog:
		next := any(after).(domain.CreationLog)
		valid = old.ID == next.ID && old.ProjectID == next.ProjectID && old.Name == next.Name && old.Path == next.Path && old.TemplateID == next.TemplateID && old.Kind == next.Kind && old.RecordedAt.Equal(next.RecordedAt) && old.Result == nil && next.Result != nil
	case domain.BootstrapRecovery:
		next := any(after).(domain.BootstrapRecovery)
		valid = next.Assessment != nil && (old.Assessment == nil || !next.Assessment.CheckedAt.Before(old.Assessment.CheckedAt))
		old.Assessment, next.Assessment = nil, nil
		valid = valid && reflect.DeepEqual(old, next)
	}
	if !valid {
		return fmt.Errorf("%w: update changed immutable identity or moved updatedAt backwards", ErrInvalidDocument)
	}
	return nil
}
