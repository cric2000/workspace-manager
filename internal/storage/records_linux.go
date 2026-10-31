package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"sync"
)

var (
	ErrConflict  = errors.New("record changed since it was read")
	ErrImmutable = errors.New("session changes require an explicit revision operation")
	ErrClosed    = errors.New("repository is closed")
)

// Revision fingerprints the exact stored bytes, including their schema version.
type Revision string

type Snapshot[T Record] struct {
	Value    T
	Revision Revision
}

// ReadError preserves a damaged record's fingerprint for explicit recovery.
// File/permission/oversize errors cannot provide a complete fingerprint.
type ReadError struct {
	Revision Revision
	Err      error
}

func (err *ReadError) Error() string { return "read repository record: " + err.Err.Error() }
func (err *ReadError) Unwrap() error { return err.Err }

// MutationError describes whether the PRIMARY record changed, even when a
// nested CommitError refers only to a history file. Committed=true requires
// reconciliation; it does not mean all durability checks succeeded.
type MutationError struct {
	Stage     string
	Committed bool
	Err       error
}

func (err *MutationError) Error() string { return "repository " + err.Stage + ": " + err.Err.Error() }
func (err *MutationError) Unwrap() error { return err.Err }

type recordStore[T Record] struct {
	root      *os.Root
	history   *os.Root
	codec     *Codec[T]
	check     func(string, T) error
	write     func(*os.Root, string, []byte) error
	lifecycle sync.RWMutex
	closed    bool
}

func openRecordStore[T Record](path string, codec *Codec[T], check func(string, T) error) (*recordStore[T], error) {
	if codec == nil || codec.version < 1 {
		return nil, errors.New("repository requires a configured codec")
	}
	root, err := OpenPrivateDirectory(path)
	if err != nil {
		return nil, err
	}
	history, err := privateChild(root, "history")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	return &recordStore[T]{
		root: root, history: history, codec: codec, check: check,
		write: func(root *os.Root, name string, data []byte) error {
			return writeAtomic(root, name, data, realAtomicOps())
		},
	}, nil
}

func (store *recordStore[T]) close() error {
	store.lifecycle.Lock()
	defer store.lifecycle.Unlock()
	if store.closed {
		return nil
	}
	store.closed = true
	return errors.Join(store.history.Close(), store.root.Close())
}

func (store *recordStore[T]) transaction(ctx context.Context, operation func() error) error {
	store.lifecycle.RLock()
	defer store.lifecycle.RUnlock()
	if store.closed {
		return ErrClosed
	}
	lock, err := lockDirectory(ctx, store.root)
	if err != nil {
		return err
	}
	defer lock.Close() // Closing this independent descriptor releases its flock.
	if err := ctx.Err(); err != nil {
		return err
	}
	return operation()
}

func revisionOf(data []byte) Revision {
	sum := sha256.Sum256(data)
	return Revision(hex.EncodeToString(sum[:]))
}

func validRevision(revision Revision) bool {
	if len(revision) != 64 {
		return false
	}
	for _, character := range revision {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func (store *recordStore[T]) decode(name string, data []byte) (Snapshot[T], error) {
	revision := revisionOf(data)
	value, err := store.codec.Decode(data)
	if err == nil {
		err = store.check(name, value)
	}
	if err != nil {
		return Snapshot[T]{}, &ReadError{Revision: revision, Err: err}
	}
	return Snapshot[T]{Value: value, Revision: revision}, nil
}

func (store *recordStore[T]) read(name string) (Snapshot[T], []byte, error) {
	data, err := readPrivateFile(store.root, name)
	if err != nil {
		return Snapshot[T]{}, nil, err
	}
	value, err := store.decode(name, data)
	return value, data, err
}

func (store *recordStore[T]) get(ctx context.Context, name string) (Snapshot[T], error) {
	var snapshot Snapshot[T]
	err := store.transaction(ctx, func() error {
		var err error
		snapshot, _, err = store.read(name)
		return err
	})
	return snapshot, err
}

func (store *recordStore[T]) archive(name string, data []byte, damaged bool) error {
	history, err := privateChild(store.history, strings.TrimSuffix(name, ".json"))
	if err != nil {
		return err
	}
	defer history.Close()
	filename := string(revisionOf(data)) + ".json"
	if damaged {
		filename = "damaged-" + filename
	}
	existing, err := readPrivateFile(history, filename)
	if err == nil {
		if !bytes.Equal(existing, data) {
			return fmt.Errorf("%w: history fingerprint mismatch", ErrInvalidDocument)
		}
		file, err := openPrivateRecord(history, filename)
		if err != nil {
			return err
		}
		err = errors.Join(file.Sync(), file.Close())
		if err != nil {
			return err
		}
		// A previous attempt may have synced the file but failed directory sync.
		return syncDirectory(history)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return store.write(history, filename, data)
}

func (store *recordStore[T]) create(ctx context.Context, name string, value T, guard func() error) error {
	data, err := store.codec.Encode(value)
	if err != nil {
		return err
	}
	if err := store.check(name, value); err != nil {
		return err
	}
	return store.transaction(ctx, func() error {
		if guard != nil {
			if err := guard(); err != nil {
				return err
			}
		}
		if _, err := store.root.Lstat(name); err == nil {
			return fs.ErrExist
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		// The first snapshot also has a recovery copy. A failed primary save may
		// leave an orphan history candidate; recovery is always an explicit choice.
		if err := store.archive(name, data, false); err != nil {
			return &MutationError{Stage: "backup", Err: err}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return store.writePrimary(name, data)
	})
}

func (store *recordStore[T]) getRevision(ctx context.Context, name string, revision Revision) (Snapshot[T], error) {
	var snapshot Snapshot[T]
	err := store.transaction(ctx, func() error {
		history, err := privateChild(store.history, strings.TrimSuffix(name, ".json"))
		if err != nil {
			return err
		}
		defer history.Close()
		snapshot, _, err = store.readRevision(name, revision, history)
		return err
	})
	return snapshot, err
}

func (store *recordStore[T]) writePrimary(name string, data []byte) error {
	err := store.write(store.root, name, data)
	if err == nil {
		return nil
	}
	var commit *CommitError
	return &MutationError{Stage: "write", Committed: errors.As(err, &commit), Err: err}
}

func (store *recordStore[T]) update(ctx context.Context, name string, expected Revision, change func(T) (T, error)) error {
	if change == nil {
		return errors.New("update callback is required")
	}
	return store.transaction(ctx, func() error {
		current, raw, err := store.read(name)
		if err != nil {
			return err // Never use defaults after a failed read or future schema.
		}
		if expected != current.Revision {
			return ErrConflict
		}
		next, err := change(current.Value)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := stableIdentity(current.Value, next); err != nil {
			return err
		}
		data, err := store.codec.Encode(next)
		if err != nil {
			return err
		}
		if err := store.check(name, next); err != nil {
			return err
		}
		if bytes.Equal(raw, data) {
			return nil
		}
		if err := store.archive(name, raw, false); err != nil {
			return &MutationError{Stage: "backup", Err: err}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return store.writePrimary(name, data)
	})
}

func (store *recordStore[T]) remove(ctx context.Context, name string, expected Revision) error {
	return store.transaction(ctx, func() error {
		current, raw, err := store.read(name)
		if err != nil {
			return err
		}
		if expected != current.Revision {
			return ErrConflict
		}
		if err := store.archive(name, raw, false); err != nil {
			return &MutationError{Stage: "backup", Err: err}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := store.root.Remove(name); err != nil {
			return &MutationError{Stage: "delete", Err: err}
		}
		if err := syncDirectory(store.root); err != nil {
			return &MutationError{Stage: "delete", Committed: true, Err: err}
		}
		return nil
	})
}

func (store *recordStore[T]) historyFor(ctx context.Context, name string) ([]Revision, error) {
	var revisions []Revision
	err := store.transaction(ctx, func() error {
		history, err := privateChild(store.history, strings.TrimSuffix(name, ".json"))
		if err != nil {
			return err
		}
		defer history.Close()
		directory, err := privateDirectory(history)
		if err != nil {
			return err
		}
		defer directory.Close()
		entries, err := directory.ReadDir(-1)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			revision := Revision(strings.TrimSuffix(entry.Name(), ".json"))
			if !strings.HasSuffix(entry.Name(), ".json") || !validRevision(revision) {
				continue // Quarantine and operation-owned temp names are not revisions.
			}
			if _, _, err := store.readRevision(name, revision, history); err != nil {
				return err
			}
			revisions = append(revisions, revision)
		}
		sort.Slice(revisions, func(i, j int) bool { return revisions[i] < revisions[j] })
		return nil
	})
	if err != nil {
		return nil, err
	}
	return revisions, nil
}

func (store *recordStore[T]) readRevision(name string, revision Revision, history *os.Root) (Snapshot[T], []byte, error) {
	if !validRevision(revision) {
		return Snapshot[T]{}, nil, errors.New("invalid history revision")
	}
	data, err := readPrivateFile(history, string(revision)+".json")
	if err != nil {
		return Snapshot[T]{}, nil, err
	}
	if revisionOf(data) != revision {
		return Snapshot[T]{}, nil, fmt.Errorf("%w: history fingerprint mismatch", ErrInvalidDocument)
	}
	snapshot, err := store.decode(name, data)
	return snapshot, data, err
}

func (store *recordStore[T]) restore(ctx context.Context, name string, expected, revision Revision) error {
	return store.transaction(ctx, func() error {
		history, err := privateChild(store.history, strings.TrimSuffix(name, ".json"))
		if err != nil {
			return err
		}
		defer history.Close()
		_, restored, err := store.readRevision(name, revision, history)
		if err != nil {
			return err
		}
		current, err := readPrivateFile(store.root, name)
		if errors.Is(err, fs.ErrNotExist) {
			if expected != "" {
				return ErrConflict
			}
		} else if err != nil {
			return err
		} else {
			if revisionOf(current) != expected {
				return ErrConflict
			}
			_, decodeErr := store.decode(name, current)
			if decodeErr != nil && !errors.Is(decodeErr, ErrInvalidDocument) && !errors.Is(decodeErr, ErrWrongKind) {
				return decodeErr // In particular: never downgrade a newer schema.
			}
			if bytes.Equal(current, restored) {
				return nil
			}
			if err := store.archive(name, current, decodeErr != nil); err != nil {
				return &MutationError{Stage: "backup", Err: err}
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return store.writePrimary(name, restored)
	})
}
