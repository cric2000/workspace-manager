package bootstrap

import (
	"context"
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
	"workspace-manager/internal/domain"
)

var (
	ErrParent  = errors.New("unsafe or unavailable parent directory")
	ErrExists  = errors.New("destination already exists")
	ErrChanged = errors.New("parent directory changed")
)

type ParentIdentity struct {
	Device uint64
	Inode  uint64
}
type Parent struct {
	file     *os.File
	Path     string
	Identity ParentIdentity
}

// OpenParent opens an existing absolute directory without following any symlink
// component. The pinned descriptor anchors every write and the final rename.
func OpenParent(path string) (*Parent, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || len(path) > 4096 || strings.ContainsRune(path, 0) {
		return nil, ErrParent
	}
	root, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrParent
	}
	defer unix.Close(root)
	fd, err := unix.Openat2(root, strings.TrimPrefix(path, "/"), &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
	if err != nil {
		return nil, ErrParent
	}
	file := os.NewFile(uintptr(fd), path)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		file.Close()
		return nil, ErrParent
	}
	return &Parent{file: file, Path: path, Identity: ParentIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}}, nil
}

func (parent *Parent) Close() error { return parent.file.Close() }
func (parent *Parent) Absent(name string) error {
	if !safeName.MatchString(name) {
		return ErrOptions
	}
	var stat unix.Stat_t
	err := unix.Fstatat(int(parent.file.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err == nil {
		return ErrExists
	}
	return ErrParent
}

func (parent *Parent) Current() bool {
	current, err := OpenParent(parent.Path)
	if err != nil {
		return false
	}
	defer current.Close()
	return current.Identity == parent.Identity
}

type Publication struct {
	Published    bool
	Durable      bool
	RecoveryPath string
}

type ownedFile struct {
	directory *os.File
	name      string
	device    uint64
	inode     uint64
	changed   unix.Timespec
}

type ownedDirectory struct {
	parent *os.File
	file   *os.File
	name   string
	device uint64
	inode  uint64
}

// Publish never replaces any existing entry, including an empty directory or
// dangling symlink. Cleanup unlinks only exact inodes created by this operation;
// unknown entries or a replaced staging directory are preserved for recovery.
func (parent *Parent) Publish(ctx context.Context, name string, files []File) (result Publication, err error) {
	if err = parent.Absent(name); err != nil {
		return result, err
	}
	seen := map[string]bool{}
	directoryNames := map[string]bool{}
	if len(files) == 0 || len(files) > 64 {
		return result, ErrOptions
	}
	total := 0
	for _, file := range files {
		parts := strings.Split(file.Path, "/")
		if !fs.ValidPath(file.Path) || file.Path == "." || strings.ContainsRune(file.Path, 0) || len(file.Path) > 1024 || len(parts) > 8 || seen[file.Path] {
			return result, ErrOptions
		}
		seen[file.Path] = true
		for i := 1; i < len(parts); i++ {
			directoryNames[strings.Join(parts[:i], "/")] = true
		}
		data, decodeErr := fileBytes(file)
		if decodeErr != nil {
			return result, decodeErr
		}
		total += len(data)
	}
	if total > 1<<20 || len(directoryNames) > 128 {
		return result, ErrOptions
	}
	for directory := range directoryNames {
		if seen[directory] {
			return result, ErrOptions
		}
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	id, err := domain.NewID()
	if err != nil {
		return result, err
	}
	stageName := ".workspace-manager-" + string(id) + ".tmp"
	parentFD := int(parent.file.Fd())
	if err = unix.Mkdirat(parentFD, stageName, 0700); err != nil {
		return result, err
	}
	result.RecoveryPath = filepath.Join(parent.Path, stageName)
	fd, err := unix.Openat(parentFD, stageName, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return result, err
	}
	stage := os.NewFile(uintptr(fd), stageName)
	defer stage.Close()
	var stageStat unix.Stat_t
	if err = unix.Fstat(fd, &stageStat); err != nil {
		return result, err
	}
	owned := []ownedFile{}
	directories := []ownedDirectory{}
	openDirectories := map[string]*os.File{".": stage}
	defer func() {
		defer func() {
			for i := len(directories) - 1; i >= 0; i-- {
				_ = directories[i].file.Close()
			}
		}()
		if result.Published {
			return
		}
		for _, file := range owned {
			var stat unix.Stat_t
			fileFD := int(file.directory.Fd())
			if unix.Fstatat(fileFD, file.name, &stat, unix.AT_SYMLINK_NOFOLLOW) == nil && uint64(stat.Dev) == file.device && stat.Ino == file.inode && stat.Ctim == file.changed {
				_ = unix.Unlinkat(fileFD, file.name, 0)
			}
		}
		for i := len(directories) - 1; i >= 0; i-- {
			directory := directories[i]
			var stat unix.Stat_t
			parentFD := int(directory.parent.Fd())
			if unix.Fstatat(parentFD, directory.name, &stat, unix.AT_SYMLINK_NOFOLLOW) == nil && uint64(stat.Dev) == directory.device && stat.Ino == directory.inode {
				_ = unix.Unlinkat(parentFD, directory.name, unix.AT_REMOVEDIR)
			}
		}
		var current unix.Stat_t
		if unix.Fstatat(parentFD, stageName, &current, unix.AT_SYMLINK_NOFOLLOW) == nil && current.Dev == stageStat.Dev && current.Ino == stageStat.Ino && unix.Unlinkat(parentFD, stageName, unix.AT_REMOVEDIR) == nil {
			result.RecoveryPath = ""
			_ = parent.file.Sync()
		}
	}()
	for _, file := range files {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		parts := strings.Split(file.Path, "/")
		directory := stage
		for i, component := range parts[:len(parts)-1] {
			relative := strings.Join(parts[:i+1], "/")
			if existing := openDirectories[relative]; existing != nil {
				directory = existing
				continue
			}
			// Existing, unowned entries are never adopted, even if they happen to
			// be directories. Every descriptor descends from our private stage.
			if err = unix.Mkdirat(int(directory.Fd()), component, 0700); err != nil {
				return result, err
			}
			childFD, openErr := unix.Openat(int(directory.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if openErr != nil {
				return result, openErr
			}
			child := os.NewFile(uintptr(childFD), relative)
			var stat unix.Stat_t
			if err = unix.Fstat(childFD, &stat); err != nil {
				_ = child.Close()
				return result, err
			}
			directories = append(directories, ownedDirectory{parent: directory, file: child, name: component, device: uint64(stat.Dev), inode: stat.Ino})
			openDirectories[relative] = child
			directory = child
		}
		base := parts[len(parts)-1]
		descriptor, openErr := unix.Openat(int(directory.Fd()), base, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
		if openErr != nil {
			return result, openErr
		}
		handle := os.NewFile(uintptr(descriptor), file.Path)
		var stat unix.Stat_t
		if err = unix.Fstat(descriptor, &stat); err == nil {
			owned = append(owned, ownedFile{directory, base, uint64(stat.Dev), stat.Ino, stat.Ctim})
			data, _ := fileBytes(file)
			_, err = handle.Write(data)
		}
		if err == nil {
			err = handle.Sync()
		}
		// Writes change ctime. Remember the final owned version; files edited or
		// replaced afterward are left for recovery rather than removed on failure.
		if finalErr := unix.Fstat(descriptor, &stat); finalErr == nil && len(owned) > 0 && owned[len(owned)-1].name == base && owned[len(owned)-1].directory == directory {
			owned[len(owned)-1].changed = stat.Ctim
		}
		err = errors.Join(err, handle.Close())
		if err != nil {
			return result, err
		}
	}
	for i := len(directories) - 1; i >= 0; i-- {
		directory := directories[i]
		var stat unix.Stat_t
		if unix.Fstatat(int(directory.parent.Fd()), directory.name, &stat, unix.AT_SYMLINK_NOFOLLOW) != nil || uint64(stat.Dev) != directory.device || stat.Ino != directory.inode {
			return result, ErrChanged
		}
		if err = directory.file.Sync(); err != nil {
			return result, err
		}
	}
	// Only publish the exact tree we built. An unexpected entry or later edit
	// is kept for recovery; it cannot silently join the reviewed starter.
	expected := map[*os.File]map[string]bool{}
	for _, directory := range openDirectories {
		expected[directory] = map[string]bool{}
	}
	for _, directory := range directories {
		expected[directory.parent][directory.name] = true
	}
	for _, file := range owned {
		expected[file.directory][file.name] = true
		var stat unix.Stat_t
		if unix.Fstatat(int(file.directory.Fd()), file.name, &stat, unix.AT_SYMLINK_NOFOLLOW) != nil || uint64(stat.Dev) != file.device || stat.Ino != file.inode || stat.Ctim != file.changed {
			return result, ErrChanged
		}
	}
	for directory, names := range expected {
		entries, readErr := directory.ReadDir(-1)
		if readErr != nil {
			return result, readErr
		}
		if len(entries) != len(names) {
			return result, ErrChanged
		}
		for _, entry := range entries {
			if !names[entry.Name()] {
				return result, ErrChanged
			}
		}
	}
	if err = stage.Sync(); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	var current unix.Stat_t
	if !parent.Current() || unix.Fstatat(parentFD, stageName, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || current.Dev != stageStat.Dev || current.Ino != stageStat.Ino {
		return result, ErrChanged
	}
	if err = unix.Renameat2(parentFD, stageName, parentFD, name, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) {
			err = ErrExists
		}
		return result, err
	}
	result.Published = true
	result.RecoveryPath = ""
	if err = parent.file.Sync(); err != nil {
		return result, err
	}
	result.Durable = true
	if !parent.Current() {
		return result, ErrChanged
	}
	return result, nil
}

// Text files preserve their literal bytes; binary assets travel through the JSON
// bridge as base64 and are decoded only after bounded validation.
func fileBytes(file File) ([]byte, error) {
	if len(file.Contents) > 2<<20 {
		return nil, ErrOptions
	}
	switch file.Encoding {
	case "":
		return []byte(file.Contents), nil
	case "base64":
		data, err := base64.StdEncoding.Strict().DecodeString(file.Contents)
		if err != nil {
			return nil, ErrOptions
		}
		return data, nil
	default:
		return nil, ErrOptions
	}
}
