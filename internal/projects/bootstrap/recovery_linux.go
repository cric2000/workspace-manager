package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
	"workspace-manager/internal/domain"
)

func RecoveryManifest(files []File) ([]domain.RecoveryFile, error) {
	manifest := make([]domain.RecoveryFile, 0, len(files))
	for _, file := range files {
		data, err := fileBytes(file)
		if err != nil {
			return nil, err
		}
		hash := sha256.Sum256(data)
		manifest = append(manifest, domain.RecoveryFile{Path: file.Path, Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:])})
	}
	return manifest, nil
}

// VerifyRecoveryTree reads only the bounded, exact manifest tree. No symlink,
// added configuration, extra directory or special file is adopted or executed.
func VerifyRecoveryTree(ctx context.Context, root *os.File, manifest []domain.RecoveryFile) error {
	files := map[string]domain.RecoveryFile{}
	directories := map[string]bool{}
	for _, file := range manifest {
		files[file.Path] = file
		for parent := filepath.ToSlash(filepath.Dir(file.Path)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			directories[parent] = true
		}
	}
	count, found := 0, 0
	total := int64(0)
	var walk func(*os.File, string) error
	walk = func(directory *os.File, prefix string) error {
		entries, err := directory.ReadDir(193)
		if err != nil && !errors.Is(err, io.EOF) {
			return ErrChanged
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			count++
			if count > 192 {
				return ErrChanged
			}
			path := prefix + entry.Name()
			fd, err := unix.Openat2(int(root.Fd()), path, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
			if err != nil {
				return ErrChanged
			}
			handle := os.NewFile(uintptr(fd), path)
			info, err := handle.Stat()
			if err != nil {
				handle.Close()
				return ErrChanged
			}
			if info.IsDir() {
				if !directories[path] {
					handle.Close()
					return ErrChanged
				}
				err = walk(handle, path+"/")
				handle.Close()
				if err != nil {
					return err
				}
				continue
			}
			expected, ok := files[path]
			if !ok || !info.Mode().IsRegular() || info.Size() != expected.Size || info.Size() > 1<<20 {
				handle.Close()
				return ErrChanged
			}
			data, err := io.ReadAll(io.LimitReader(handle, (1<<20)+1))
			handle.Close()
			total += int64(len(data))
			if err != nil || int64(len(data)) != expected.Size || total > 1<<20 {
				return ErrChanged
			}
			hash := sha256.Sum256(data)
			if !strings.EqualFold(hex.EncodeToString(hash[:]), expected.SHA256) {
				return ErrChanged
			}
			found++
		}
		return nil
	}
	fd, err := unix.Openat(int(root.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	listing := os.NewFile(uintptr(fd), root.Name())
	defer listing.Close()
	if err := walk(listing, ""); err != nil {
		return err
	}
	if found != len(files) {
		return ErrChanged
	}
	return nil
}
