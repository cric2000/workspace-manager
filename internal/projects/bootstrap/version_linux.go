package bootstrap

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"workspace-manager/internal/domain"
)

// RunVersion runs a fixed, reviewed version invocation outside the project. It
// requests no downloads and excludes host startup/configuration environment.
func RunVersion(ctx context.Context, action domain.CommandAction, node string) CommandResult {
	result := CommandResult{ExitCode: -1}
	temporary, err := os.MkdirTemp("", "workspace-manager-version-")
	if err != nil {
		result.Err = err
		return result
	}
	defer os.RemoveAll(temporary)
	for _, name := range []string{"home", "config", "cache", "bin"} {
		if err := os.Mkdir(filepath.Join(temporary, name), 0700); err != nil {
			result.Err = err
			return result
		}
	}
	if node != "" {
		if err := os.Symlink(node, filepath.Join(temporary, "bin", "node")); err != nil {
			result.Err = err
			return result
		}
	}
	directory, err := os.Open(temporary)
	if err != nil {
		result.Err = err
		return result
	}
	defer directory.Close()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, action.Executable, action.Arguments...)
	command.Dir = "/proc/" + strconv.Itoa(os.Getpid()) + "/fd/" + strconv.Itoa(int(directory.Fd()))
	command.Env = []string{
		"PATH=" + filepath.Join(temporary, "bin"), "HOME=" + filepath.Join(temporary, "home"),
		"XDG_CONFIG_HOME=" + filepath.Join(temporary, "config"), "XDG_CACHE_HOME=" + filepath.Join(temporary, "cache"),
		"TMPDIR=" + temporary, "LANG=C", "LC_ALL=C", "NO_COLOR=1", "CI=1",
		"GOTOOLCHAIN=local", "GOENV=off", "GOWORK=off", "GOPROXY=off", "GOSUMDB=off",
		"COREPACK_ENABLE_NETWORK=0", "COREPACK_ENABLE_DOWNLOAD_PROMPT=0", "COREPACK_ENABLE_PROJECT_SPEC=0",
		"npm_config_cache=" + filepath.Join(temporary, "cache"), "npm_config_userconfig=" + filepath.Join(temporary, "user.npmrc"),
		"npm_config_globalconfig=" + filepath.Join(temporary, "global.npmrc"), "npm_config_update_notifier=false",
		"PNPM_HOME=" + filepath.Join(temporary, "bin"), "PIP_CONFIG_FILE=/dev/null", "PIP_DISABLE_PIP_VERSION_CHECK=1",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0",
	}
	output := &versionOutput{}
	command.Stdout, command.Stderr = output, output
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = 250 * time.Millisecond
	if err := command.Start(); err != nil {
		result.Err = err
		return result
	}
	result.Started = true
	result.Err = command.Wait()
	if ctx.Err() != nil {
		result.Err = ctx.Err()
	}
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	output.mu.Lock()
	result.Output, result.Truncated = string(output.data), output.truncated
	output.mu.Unlock()
	return result
}

type versionOutput struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (output *versionOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	count := len(data)
	remaining := 4096 - len(output.data)
	if len(data) > remaining {
		output.truncated = true
		data = data[:remaining]
	}
	output.data = append(output.data, data...)
	return count, nil
}
