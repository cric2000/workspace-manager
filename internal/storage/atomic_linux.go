package storage

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"syscall"
)

// CommitError means rename succeeded: the new file is visible, but directory
// sync failed. Callers must reconcile/re-read rather than assume rollback.
type CommitError struct{ Err error }

func (err *CommitError) Error() string {
	return "storage replacement is visible but crash durability is uncertain: " + err.Err.Error()
}

func (err *CommitError) Unwrap() error { return err.Err }

// Read loads a bounded, private regular file beneath an already opened root.
// It never recovers a backup or writes a migrated document implicitly.
func (codec *Codec[T]) Read(root *os.Root, name string) (T, error) {
	var zero T
	data, err := readPrivateFile(root, name)
	if err != nil {
		return zero, err
	}
	return codec.Decode(data)
}

func readPrivateFile(root *os.Root, name string) ([]byte, error) {
	file, err := openPrivateRecord(root, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxDocumentBytes {
		return nil, ErrDocumentTooLarge
	}
	return data, nil
}

func openPrivateRecord(root *os.Root, name string) (*os.File, error) {
	if err := validFilename(name); err != nil {
		return nil, err
	}
	directory, err := privateDirectory(root)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	// NONBLOCK ensures an unexpected FIFO cannot hang the controller before Stat.
	// Root follows in-tree symlinks during resolution. A single validated name
	// opened directly at the pinned directory enforces kernel-level NOFOLLOW.
	descriptor, err := syscall.Openat(int(directory.Fd()), name, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(descriptor), name)
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := privateEntry(info, false); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// Write validates and encodes before touching the directory, then replaces one
// file atomically. It requires a caller-owned 0700 directory. Readers see complete
// old or new files; repository transactions/locking and backups are separate.
func (codec *Codec[T]) Write(root *os.Root, name string, value T) error {
	data, err := codec.Encode(value)
	if err != nil {
		return err
	}
	return writeAtomic(root, name, data, realAtomicOps())
}

// These narrow operation hooks exercise disk-error boundaries without replacing
// a filesystem or changing package globals. Production always uses realAtomicOps.
type atomicOps struct {
	write   func(*os.File, []byte) (int, error)
	sync    func(*os.File) error
	close   func(*os.File) error
	rename  func(*os.Root, string, string) error
	syncDir func(*os.File) error
}

func realAtomicOps() atomicOps {
	return atomicOps{
		write: (*os.File).Write, sync: (*os.File).Sync, close: (*os.File).Close,
		rename: (*os.Root).Rename, syncDir: (*os.File).Sync,
	}
}

func writeAtomic(root *os.Root, name string, data []byte, ops atomicOps) (result error) {
	if err := validFilename(name); err != nil {
		return err
	}
	directory, err := privateDirectory(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	if info, err := root.Lstat(name); err == nil {
		if err := privateEntry(info, false); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// O_EXCL proves that this operation created the temporary file. Neither the
	// old target nor arbitrary files matching a prefix are ever cleanup targets.
	temporary := ".pending-" + rand.Text()
	file, err := root.OpenFile(temporary, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			if err := root.Remove(temporary); err != nil && !errors.Is(err, fs.ErrNotExist) {
				result = errors.Join(result, fmt.Errorf("remove owned temporary file: %w", err))
			}
		}
	}()
	// Use the owned descriptor, never a path-based chmod that could follow a link.
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	written, err := ops.write(file, data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	if err := ops.sync(file); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	verified, err := io.ReadAll(io.LimitReader(file, MaxDocumentBytes+1))
	if err != nil {
		return err
	}
	if !bytes.Equal(verified, data) {
		return errors.New("temporary file verification failed")
	}
	if err := ops.close(file); err != nil {
		return err
	}
	if err := ops.rename(root, temporary, name); err != nil {
		return err
	}
	committed = true
	if err := ops.syncDir(directory); err != nil {
		return &CommitError{Err: err}
	}
	return nil
}

func privateDirectory(root *os.Root) (*os.File, error) {
	if root == nil {
		return nil, errors.New("an opened storage root is required")
	}
	directory, err := root.OpenFile(".", os.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	info, err := directory.Stat()
	if err == nil {
		err = privateEntry(info, true)
	}
	if err != nil {
		_ = directory.Close()
		return nil, err
	}
	return directory, nil
}

func privateEntry(info fs.FileInfo, directory bool) error {
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return errors.New("storage entry must be owned by the current user")
	}
	if directory {
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			return errors.New("storage directory must have mode 0700")
		}
	} else if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("storage file must be regular with mode 0600")
	}
	return nil
}

func validFilename(name string) error {
	stem, ok := strings.CutSuffix(name, ".json")
	if !ok || stem == "" || len(name) > 128 {
		return errors.New("storage filename must be one lowercase JSON filename")
	}
	for _, character := range stem {
		if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' || character == '_') {
			return errors.New("storage filename must be one lowercase JSON filename")
		}
	}
	return nil
}
