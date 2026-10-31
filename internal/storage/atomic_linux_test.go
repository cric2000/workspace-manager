package storage

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"workspace-manager/internal/domain"
)

func storageRoot(t *testing.T) (*os.Root, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "records")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root, path
}

func TestAtomicWriteReadAndReplacement(t *testing.T) {
	root, path := storageRoot(t)
	codec := codecFor[domain.Settings](t)
	initial := domain.DefaultSettings()
	if err := codec.Write(root, "settings.json", initial); err != nil {
		t.Fatal(err)
	}
	updated := initial
	updated.Appearance = domain.AppearanceDark
	if err := codec.Write(root, "settings.json", updated); err != nil {
		t.Fatal(err)
	}
	value, err := codec.Read(root, "settings.json")
	if err != nil || value != updated {
		t.Fatalf("replacement did not round trip: %+v / %v", value, err)
	}
	info, err := os.Stat(filepath.Join(path, "settings.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("saved file is not private: %v / %v", info, err)
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files remain after commit: %v / %v", entries, err)
	}
	if _, err := codec.Read(root, "missing.json"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file error was lost: %v", err)
	}
}

func TestInvalidSaveNeverChangesExistingData(t *testing.T) {
	root, path := storageRoot(t)
	codec := codecFor[domain.Settings](t)
	if err := codec.Write(root, "settings.json", domain.DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	before, err := root.ReadFile("settings.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := codec.Write(root, "settings.json", domain.Settings{}); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("invalid save was accepted: %v", err)
	}
	after, err := root.ReadFile("settings.json")
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("invalid save modified the previous file")
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 1 {
		t.Fatal("invalid save created a temporary file")
	}
}

func TestPreCommitFailuresPreserveLastGoodAndOnlyCleanOwnedFiles(t *testing.T) {
	failure := errors.New("simulated disk failure")
	for _, tc := range []struct {
		name  string
		alter func(*atomicOps)
		want  error
	}{
		{"partial write error", func(ops *atomicOps) {
			ops.write = func(file *os.File, data []byte) (int, error) {
				n, _ := file.Write(data[:len(data)/2])
				return n, failure
			}
		}, failure},
		{"short write", func(ops *atomicOps) {
			ops.write = func(file *os.File, data []byte) (int, error) { return file.Write(data[:len(data)-1]) }
		}, io.ErrShortWrite},
		{"file sync", func(ops *atomicOps) { ops.sync = func(*os.File) error { return failure } }, failure},
		{"verification", func(ops *atomicOps) {
			ops.write = func(file *os.File, data []byte) (int, error) {
				altered := bytes.Clone(data)
				altered[0] = '['
				return file.Write(altered)
			}
		}, nil},
		{"close", func(ops *atomicOps) {
			ops.close = func(file *os.File) error { _ = file.Close(); return failure }
		}, failure},
		{"rename", func(ops *atomicOps) { ops.rename = func(*os.Root, string, string) error { return failure } }, failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, path := storageRoot(t)
			codec := codecFor[domain.Settings](t)
			if err := codec.Write(root, "settings.json", domain.DefaultSettings()); err != nil {
				t.Fatal(err)
			}
			before, err := root.ReadFile("settings.json")
			if err != nil {
				t.Fatal(err)
			}
			// An unrelated file with a similar prefix must survive cleanup.
			if err := root.WriteFile(".pending-user-owned", []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			updated := domain.DefaultSettings()
			updated.Appearance = domain.AppearanceDark
			data, err := codec.Encode(updated)
			if err != nil {
				t.Fatal(err)
			}
			ops := realAtomicOps()
			tc.alter(&ops)
			err = writeAtomic(root, "settings.json", data, ops)
			var committed *CommitError
			if err == nil || errors.As(err, &committed) || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("expected pre-commit failure %v, got %v", tc.want, err)
			}
			after, err := root.ReadFile("settings.json")
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("last-good file changed: %v", err)
			}
			value, err := codec.Read(root, "settings.json")
			if err != nil || value != domain.DefaultSettings() {
				t.Fatalf("old record is not readable: %+v / %v", value, err)
			}
			entries, err := os.ReadDir(path)
			if err != nil || len(entries) != 2 {
				t.Fatalf("cleanup affected unrelated files or leaked a temp: %v / %v", entries, err)
			}
			unrelated, err := root.ReadFile(".pending-user-owned")
			if err != nil || string(unrelated) != "keep" {
				t.Fatal("cleanup removed an unrelated file")
			}
		})
	}
}

func TestPostCommitSyncFailureReportsVisibleReplacement(t *testing.T) {
	root, _ := storageRoot(t)
	codec := codecFor[domain.Settings](t)
	if err := codec.Write(root, "settings.json", domain.DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	updated := domain.DefaultSettings()
	updated.Appearance = domain.AppearanceLight
	data, err := codec.Encode(updated)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("directory sync failure")
	ops := realAtomicOps()
	ops.syncDir = func(*os.File) error { return failure }
	err = writeAtomic(root, "settings.json", data, ops)
	var committed *CommitError
	if !errors.As(err, &committed) || !errors.Is(err, failure) {
		t.Fatalf("post-commit failure was not distinguished: %v", err)
	}
	value, err := codec.Read(root, "settings.json")
	if err != nil || value != updated {
		t.Fatalf("visible replacement was rolled back or removed: %+v / %v", value, err)
	}
}

func TestStorageRejectsTraversalSymlinksAndNonPrivateEntries(t *testing.T) {
	root, path := storageRoot(t)
	codec := codecFor[domain.Settings](t)
	for _, name := range []string{"../outside.json", "/outside.json", "nested/settings.json", "", ".json", "settings.JSON", "settings.json\x00"} {
		if err := codec.Write(root, name, domain.DefaultSettings()); err == nil {
			t.Errorf("accepted invalid write name %q", name)
		}
		if _, err := codec.Read(root, name); err == nil {
			t.Errorf("accepted invalid read name %q", name)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, settingsV1, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink(outside, "link.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Read(root, "link.json"); err == nil {
		t.Fatal("read followed a symlink")
	}
	if err := codec.Write(root, "link.json", domain.DefaultSettings()); err == nil {
		t.Fatal("write accepted a symlink target")
	}
	outsideAfter, err := os.ReadFile(outside)
	if err != nil || !bytes.Equal(outsideAfter, settingsV1) {
		t.Fatal("outside file was modified")
	}
	if err := codec.Write(root, "settings.json", domain.DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink("settings.json", "internal.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Read(root, "internal.json"); err == nil {
		t.Fatal("read followed an internal symlink")
	}
	if err := codec.Write(root, "internal.json", domain.DefaultSettings()); err == nil {
		t.Fatal("write accepted an internal symlink")
	}
	if err := root.Mkdir("directory.json", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := codec.Write(root, "directory.json", domain.DefaultSettings()); err == nil {
		t.Fatal("write accepted a directory target")
	}
	if err := root.WriteFile("public.json", settingsV1, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := root.Chmod("public.json", 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Read(root, "public.json"); err == nil {
		t.Fatal("accepted a public record file")
	}
	if err := codec.Write(root, "public.json", domain.DefaultSettings()); err == nil {
		t.Fatal("replaced a non-private existing file")
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := codec.Write(root, "settings.json", domain.DefaultSettings()); err == nil {
		t.Fatal("accepted a non-private storage directory")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatal("writer silently changed existing directory permissions")
	}
	if err := codec.Write(nil, "settings.json", domain.DefaultSettings()); err == nil {
		t.Fatal("accepted a nil storage root")
	}
}

func TestStorageReadIsBoundedAndDoesNotOpenSpecialFiles(t *testing.T) {
	root, path := storageRoot(t)
	codec := codecFor[domain.Settings](t)
	if err := root.WriteFile("large.json", bytes.Repeat([]byte(" "), MaxDocumentBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Read(root, "large.json"); !errors.Is(err, ErrDocumentTooLarge) {
		t.Fatalf("read did not enforce document limit: %v", err)
	}
	if err := root.WriteFile("corrupt.json", []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Read(root, "corrupt.json"); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("read accepted corrupt data: %v", err)
	}
	if err := syscall.Mkfifo(filepath.Join(path, "fifo.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := codec.Read(root, "fifo.json"); result <- err }()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("accepted FIFO as a record")
		}
	case <-time.After(time.Second):
		t.Fatal("read blocked on a FIFO")
	}
}

func TestPinnedRootSurvivesPathReplacement(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "records")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	moved := filepath.Join(base, "moved")
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	codec := codecFor[domain.Settings](t)
	if err := codec.Write(root, "settings.json", domain.DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, "settings.json")); err != nil {
		t.Fatal("writer did not use the pinned directory", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("writer followed a replacement directory symlink")
	}
}

func TestConcurrentReplacementsNeverExposePartialJSON(t *testing.T) {
	root, path := storageRoot(t)
	codec := codecFor[domain.Settings](t)
	if err := codec.Write(root, "settings.json", domain.DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	failures := make(chan error, 8)
	for worker := range 4 {
		other, err := os.OpenRoot(path)
		if err != nil {
			t.Fatal(err)
		}
		group.Add(1)
		go func() {
			defer group.Done()
			defer other.Close()
			for iteration := range 10 {
				settings := domain.DefaultSettings()
				settings.InitialDelaySeconds = worker*10 + iteration
				if err := codec.Write(other, "settings.json", settings); err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	finished := make(chan struct{})
	go func() { group.Wait(); close(finished) }()
	for {
		value, err := codec.Read(root, "settings.json")
		if err != nil || value.Validate() != nil {
			group.Wait()
			t.Fatalf("reader observed partial/invalid JSON: %+v / %v", value, err)
		}
		select {
		case <-finished:
			close(failures)
			for err := range failures {
				t.Error(err)
			}
			entries, err := os.ReadDir(path)
			if err != nil || len(entries) != 1 {
				t.Fatalf("concurrent writers leaked temporary files: %v / %v", entries, err)
			}
			return
		default:
		}
	}
}

func TestReadRejectsFutureSchemaWithoutChangingTheFile(t *testing.T) {
	root, _ := storageRoot(t)
	codec := codecFor[domain.Settings](t)
	future := bytes.Replace(settingsV1, []byte(`"schemaVersion": 1`), []byte(`"schemaVersion": 2`), 1)
	if err := root.WriteFile("settings.json", future, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Read(root, "settings.json"); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("future schema was accepted: %v", err)
	}
	after, err := root.ReadFile("settings.json")
	if err != nil || !bytes.Equal(future, after) {
		t.Fatal("read changed an unsupported document")
	}
}

type ownershipInfo struct {
	fs.FileInfo
	owner syscall.Stat_t
}

func (info ownershipInfo) Sys() any { return &info.owner }

func TestStorageRejectsAnEntryOwnedByAnotherUser(t *testing.T) {
	_, path := storageRoot(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	untrusted := ownershipInfo{FileInfo: info, owner: syscall.Stat_t{Uid: uint32(os.Geteuid()) + 1}}
	if err := privateEntry(untrusted, true); err == nil {
		t.Fatal("accepted a storage directory owned by another user")
	}
}
