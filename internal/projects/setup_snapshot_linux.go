package projects

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"workspace-manager/internal/projects/bootstrap"
)

var errSetupExists = errors.New("setup output already exists")
var errSetupSource = errors.New("starter inputs changed")

func openSetupDirectory(path string) (*os.File, error) {
	slash, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(slash)
	fd, err := unix.Openat2(slash, strings.TrimPrefix(path, "/"), &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
	if err != nil {
		return nil, errDirectory
	}
	return os.NewFile(uintptr(fd), path), nil
}
func setupFileID(path string, info fs.FileInfo) fileIdentity {
	stat := info.Sys().(*syscall.Stat_t)
	id := fileIdentity{Path: path, Device: uint64(stat.Dev), Inode: stat.Ino, Mode: stat.Mode}
	if info.Mode().IsRegular() {
		id.Size, id.Modified, id.Changed = stat.Size, stat.Mtim, stat.Ctim
	}
	return id
}
func setupAbsent(directory *os.File, names ...string) error {
	for _, name := range names {
		var info unix.Stat_t
		err := unix.Fstatat(int(directory.Fd()), name, &info, unix.AT_SYMLINK_NOFOLLOW)
		if err == nil {
			return errSetupExists
		}
		if !errors.Is(err, unix.ENOENT) {
			return errSetupSource
		}
	}
	return nil
}
func setupRegularID(root *os.File, relative, path string) (fileIdentity, error) {
	fd, err := unix.Openat2(int(root.Fd()), relative, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_NONBLOCK | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
	if err != nil {
		return fileIdentity{}, errSetupSource
	}
	handle := os.NewFile(uintptr(fd), relative)
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fileIdentity{}, errSetupSource
	}
	return setupFileID(path, info), nil
}

// Dependency installs are offered only for the unchanged, just-created starter.
// Existing repositories/environments are accepted solely when this creation flow
// made them successfully; unrelated files, config hooks and links are refused.
func setupSnapshot(creation *pendingCreation, kind string, root *os.File) ([]fileIdentity, error) {
	info, err := root.Stat()
	if err != nil {
		return nil, errDirectory
	}
	id := setupFileID(creation.project.Path, info)
	if creation.publishedIdentity == nil || id != *creation.publishedIdentity {
		return nil, errReview
	}
	ids := []fileIdentity{id}
	switch kind {
	case "git":
		return ids, setupAbsent(root, ".git")
	case "environment":
		return ids, setupAbsent(root, ".venv")
	}
	if creation.review.Plan.TemplateID == "fastapi" {
		if creation.environmentIdentity == nil || creation.environmentConfig == nil {
			return nil, errSetupSource
		}
		current, err := identity(filepath.Join(creation.project.Path, ".venv"), false)
		if err != nil || current != *creation.environmentIdentity {
			return nil, errSetupSource
		}
		ids = append(ids, current)
		config, err := setupRegularID(root, ".venv/pyvenv.cfg", filepath.Join(creation.project.Path, ".venv/pyvenv.cfg"))
		if err != nil || config != *creation.environmentConfig {
			return nil, errSetupSource
		}
		ids = append(ids, config)
		if err := setupAbsent(root, ".venv/pip.conf"); err != nil {
			return nil, errSetupSource
		}
	} else if err := setupAbsent(root, "node_modules", "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml"); err != nil {
		return nil, err
	}
	expected := map[string]bootstrap.File{}
	directories := map[string]bool{}
	for _, file := range creation.review.Plan.Files {
		expected[file.Path] = file
		for parent := filepath.ToSlash(filepath.Dir(file.Path)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			directories[parent] = true
		}
	}
	count, found := 0, 0
	var walk func(*os.File, string) error
	walk = func(directory *os.File, prefix string) error {
		entries, err := directory.ReadDir(193)
		if err != nil && !errors.Is(err, io.EOF) {
			return errSetupSource
		}
		slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
		for _, entry := range entries {
			count++
			if count > 192 {
				return errSetupSource
			}
			relative := prefix + entry.Name()
			absolute := filepath.Join(creation.project.Path, relative)
			if prefix == "" && (entry.Name() == ".git" || entry.Name() == ".venv") {
				owned := creation.gitIdentity
				if entry.Name() == ".venv" {
					owned = creation.environmentIdentity
				}
				current, err := identity(absolute, false)
				if owned == nil || err != nil || current != *owned {
					return errSetupSource
				}
				continue
			}
			fd, err := unix.Openat2(int(root.Fd()), relative, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_NONBLOCK | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
			if err != nil {
				return errSetupSource
			}
			handle := os.NewFile(uintptr(fd), relative)
			info, err := handle.Stat()
			if err != nil {
				handle.Close()
				return errSetupSource
			}
			if info.IsDir() {
				if !directories[relative] {
					handle.Close()
					return errSetupSource
				}
				ids = append(ids, setupFileID(absolute, info))
				err = walk(handle, relative+"/")
				handle.Close()
				if err != nil {
					return err
				}
				continue
			}
			file, ok := expected[relative]
			if !ok || !info.Mode().IsRegular() || info.Size() > 1<<20 {
				handle.Close()
				return errSetupSource
			}
			data, err := io.ReadAll(io.LimitReader(handle, (1<<20)+1))
			handle.Close()
			if err != nil {
				return errSetupSource
			}
			expectedBytes := []byte(file.Contents)
			if file.Encoding == "base64" {
				expectedBytes, err = base64.StdEncoding.DecodeString(file.Contents)
			}
			if err != nil || !bytes.Equal(data, expectedBytes) {
				return errSetupSource
			}
			ids = append(ids, setupFileID(absolute, info))
			found++
		}
		return nil
	}
	// ReadDir offsets belong to the open file description; use an independent fd
	// so later source checks can enumerate the same pinned directory again.
	fd, err := unix.Openat(int(root.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errSetupSource
	}
	listing := os.NewFile(uintptr(fd), root.Name())
	defer listing.Close()
	if err := walk(listing, ""); err != nil {
		return nil, err
	}
	if found != len(expected) {
		return nil, errSetupSource
	}
	return ids, nil
}
