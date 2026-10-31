package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivateDirectoryCreatesAncestorsAndRefusesSymlinks(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "config", "workspace-manager", "profiles")
	root, err := OpenPrivateDirectory(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, relative := range []string{"config", "config/workspace-manager", "config/workspace-manager/profiles"} {
		info, err := os.Stat(filepath.Join(base, relative))
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("new directory is not private: %v / %v", info, err)
		}
	}
	outside := t.TempDir()
	link := filepath.Join(base, "alias")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if escaped, err := OpenPrivateDirectory(filepath.Join(link, "records")); err == nil {
		escaped.Close()
		t.Fatal("followed a symlink ancestor")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("directory setup modified a symlink destination")
	}
	for _, invalid := range []string{"relative", "/", path + "/../elsewhere", path + "\x00"} {
		if root, err := OpenPrivateDirectory(invalid); err == nil {
			root.Close()
			t.Errorf("accepted unsafe directory %q", invalid)
		}
	}
}

func TestPrivateDirectoryNeverChangesExistingPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "public")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if root, err := OpenPrivateDirectory(path); err == nil {
		root.Close()
		t.Fatal("accepted a public application directory")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatal("changed existing directory permissions")
	}
}

func TestDirectoryLockCancellationAndDescriptorIsolation(t *testing.T) {
	root, _ := storageRoot(t)
	first, err := lockDirectory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if second, err := lockDirectory(ctx, root); !errors.Is(err, context.DeadlineExceeded) {
		if second != nil {
			second.Close()
		}
		t.Fatalf("independent descriptor bypassed lock or failed cancellation: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := lockDirectory(context.Background(), root)
	if err != nil {
		t.Fatal("lock did not release on close", err)
	}
	third.Close()
	if info, err := root.Lstat(".repository.lock"); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("lock inode was removed or is not private")
	}
}

func TestDirectoryLockRejectsSymlinkWithoutFollowingIt(t *testing.T) {
	root, _ := storageRoot(t)
	if err := root.WriteFile("other.json", []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink("other.json", ".repository.lock"); err != nil {
		t.Fatal(err)
	}
	if lock, err := lockDirectory(context.Background(), root); err == nil {
		lock.Close()
		t.Fatal("followed an internal lock symlink")
	}
	data, err := root.ReadFile("other.json")
	if err != nil || string(data) != "keep" {
		t.Fatal("lock setup changed an unrelated file")
	}
}
