package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// OpenPrivateDirectory provisions a private app directory without following any
// symlink component or changing permissions on existing directories. Callers own
// the returned root. Missing ancestors are created with 0700 and parent-synced.
func OpenPrivateDirectory(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || strings.ContainsRune(path, 0) {
		return nil, errors.New("storage directory must be an absolute clean path below filesystem root")
	}
	descriptor, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	current := os.NewFile(uintptr(descriptor), "/")
	defer func() { _ = current.Close() }()
	for _, component := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		next, err := openOrCreateDirectory(current, component)
		if err != nil {
			return nil, err
		}
		_ = current.Close()
		current = next
	}
	info, err := current.Stat()
	if err != nil {
		return nil, err
	}
	if err := privateEntry(info, true); err != nil {
		return nil, err
	}
	return rootFromDescriptor(current)
}

func rootFromDescriptor(directory *os.File) (*os.Root, error) {
	// Linux /proc opens this already pinned descriptor, not the original pathname.
	// Root methods retain their own descriptor after directory is closed.
	return os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", directory.Fd()))
}

func openOrCreateDirectory(parent *os.File, name string) (*os.File, error) {
	flags := syscall.O_RDONLY | syscall.O_DIRECTORY | syscall.O_NOFOLLOW | syscall.O_CLOEXEC
	descriptor, err := syscall.Openat(int(parent.Fd()), name, flags, 0)
	if errors.Is(err, fs.ErrNotExist) {
		if err := syscall.Mkdirat(int(parent.Fd()), name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		descriptor, err = syscall.Openat(int(parent.Fd()), name, flags, 0)
	}
	if err != nil {
		return nil, err
	}
	// Also sync on retry when an earlier mkdir was visible but its parent sync
	// failed. A successful provisioning call must not depend on that attempt.
	if err := parent.Sync(); err != nil {
		_ = syscall.Close(descriptor)
		return nil, err
	}
	return os.NewFile(uintptr(descriptor), name), nil
}

func privateChild(parent *os.Root, name string) (*os.Root, error) {
	// All callers supply fixed names or validated IDs; no directory traversal.
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return nil, errors.New("invalid child directory name")
	}
	directory, err := privateDirectory(parent)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	child, err := openOrCreateDirectory(directory, name)
	if err != nil {
		return nil, err
	}
	defer child.Close()
	info, err := child.Stat()
	if err != nil {
		return nil, err
	}
	if err := privateEntry(info, true); err != nil {
		return nil, err
	}
	return rootFromDescriptor(child)
}

func syncDirectory(root *os.Root) error {
	directory, err := privateDirectory(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

// lockDirectory uses a fresh open-file description on each operation, so flock
// coordinates separate goroutines, repository handles and OS processes alike.
// The lock inode is persistent: never unlink it on unlock or during cleanup.
func lockDirectory(ctx context.Context, root *os.Root) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory, err := privateDirectory(root)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	descriptor, err := syscall.Openat(int(directory.Fd()), ".repository.lock", syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	lock := os.NewFile(uintptr(descriptor), ".repository.lock")
	info, err := lock.Stat()
	if err == nil {
		err = privateEntry(info, false)
	}
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = lock.Close()
			return nil, err
		}
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			_ = lock.Close()
			return nil, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = lock.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

var ErrLocked = errors.New("operation is owned by another live handle")

// TryDirectoryLock returns a caller-owned lease or ErrLocked without waiting.
// The lock inode must never be removed or replaced while the feature uses it.
func TryDirectoryLock(root *os.Root) (*os.File, error) {
	directory, err := privateDirectory(root)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	fd, err := syscall.Openat(int(directory.Fd()), ".repository.lock", syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	lock := os.NewFile(uintptr(fd), ".repository.lock")
	info, err := lock.Stat()
	if err == nil {
		err = privateEntry(info, false)
	}
	if err == nil {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return lock, nil
}

// WithDirectoryLock coordinates a feature operation spanning repositories.
// Use a dedicated private directory, never a repository's own directory. All
// participants must take this lock before repository locks; it is not reentrant.
// The caller owns root and must keep it open until the operation returns.
func WithDirectoryLock(ctx context.Context, root *os.Root, operation func() error) error {
	lock, err := lockDirectory(ctx, root)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	return operation()
}
