//go:build linux

package linux

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"workspace-manager/internal/applications"
	"workspace-manager/internal/domain"
)

// CustomLauncher starts explicitly reviewed external desktop tools. It does not
// supervise project commands, capture output, stop processes or infer readiness.
type CustomLauncher struct{}

func (CustomLauncher) ReviewCustom(ctx context.Context, custom domain.CustomApplication) (applications.CustomLaunchSpec, error) {
	if err := ctx.Err(); err != nil {
		return applications.CustomLaunchSpec{}, err
	}
	if err := custom.Validate(); err != nil {
		return applications.CustomLaunchSpec{}, err
	}
	custom.Arguments = slices.Clone(custom.Arguments)
	executable, err := filepath.EvalSymlinks(custom.Executable)
	if err != nil {
		return applications.CustomLaunchSpec{}, err
	}
	cwd, err := filepath.EvalSymlinks(custom.WorkingDirectory)
	if err != nil {
		return applications.CustomLaunchSpec{}, err
	}
	file, err := os.Stat(executable)
	if err != nil {
		return applications.CustomLaunchSpec{}, err
	}
	directory, err := os.Stat(cwd)
	if err != nil {
		return applications.CustomLaunchSpec{}, err
	}
	if !file.Mode().IsRegular() || file.Mode().Perm()&0o111 == 0 || !directory.IsDir() {
		return applications.CustomLaunchSpec{}, applications.ErrUnavailable
	}
	// Include resolved paths, configuration, inode/device and metadata. Replacement
	// between review/dispatch invalidates approval. This is not an executable sandbox.
	fileStat, directoryStat := file.Sys().(*syscall.Stat_t), directory.Sys().(*syscall.Stat_t)
	type identity struct {
		Device, Inode     uint64
		Mode              uint32
		Size              int64
		Modified, Changed syscall.Timespec
	}
	fileIdentity := identity{fileStat.Dev, fileStat.Ino, fileStat.Mode, fileStat.Size, fileStat.Mtim, fileStat.Ctim}
	directoryIdentity := identity{Device: directoryStat.Dev, Inode: directoryStat.Ino, Mode: directoryStat.Mode}
	snapshot := struct {
		Application           domain.CustomApplication
		Executable, Directory string
		File, CWD             identity
	}{custom, executable, cwd, fileIdentity, directoryIdentity}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return applications.CustomLaunchSpec{}, err
	}
	hash := sha256.Sum256(data)
	if err := ctx.Err(); err != nil {
		return applications.CustomLaunchSpec{}, err
	}
	return applications.CustomLaunchSpec{Application: custom, Fingerprint: hex.EncodeToString(hash[:])}, nil
}

func (launcher CustomLauncher) LaunchCustom(ctx context.Context, reviewed applications.CustomLaunchSpec) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := launcher.ReviewCustom(ctx, reviewed.Application)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return applications.ErrUnavailable
	}
	if current.Fingerprint != reviewed.Fingerprint {
		return applications.ErrLaunchChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	custom := reviewed.Application
	command := exec.Command(custom.Executable, custom.Arguments...)
	command.Dir = custom.WorkingDirectory
	for _, variable := range os.Environ() {
		key, _, _ := strings.Cut(variable, "=")
		if slices.Contains([]string{"GIO_LAUNCHED_DESKTOP_FILE", "GIO_LAUNCHED_DESKTOP_FILE_PID", "DESKTOP_STARTUP_ID", "XDG_ACTIVATION_TOKEN"}, key) {
			continue
		}
		command.Env = append(command.Env, variable)
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Nil streams use /dev/null. No command output enters logs or the WebView.
	// CommandContext would kill an external application when the GUI closes.
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }() // Reap while GUI lives; no readiness claim.
	return nil
}
