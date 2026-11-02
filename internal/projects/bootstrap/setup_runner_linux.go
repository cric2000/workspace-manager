package bootstrap

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"workspace-manager/internal/domain"
)

// CommandResult retains only bounded display output; durable task history is a
// separate milestone. Started distinguishes a spawn failure from partial setup.
type CommandResult struct {
	Started   bool
	ExitCode  int
	Output    string
	Truncated bool
	Err       error
}

// RunSetup executes one held setup invocation in the pinned project directory.
// It never interprets shell text and never removes project files on failure.
func RunSetup(ctx context.Context, action domain.CommandAction, node string, project *os.File) CommandResult {
	result := CommandResult{ExitCode: -1}
	temporary, err := os.MkdirTemp("", "workspace-manager-setup-")
	if err != nil {
		result.Err = err
		return result
	}
	defer os.RemoveAll(temporary) // exclusively created tool caches/configuration
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
	env := scaffoldEnvironment(temporary)
	env = append(env, "PYTHONNOUSERSITE=1", "PYTHONDONTWRITEBYTECODE=1", "PIP_CONFIG_FILE=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0")
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, action.Executable, action.Arguments...)
	command.Dir = "/proc/" + strconv.Itoa(os.Getpid()) + "/fd/" + strconv.Itoa(int(project.Fd()))
	command.Env = env
	output := &setupOutput{}
	command.Stdout, command.Stderr = output, output
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = 2 * time.Second
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
	result.Output, result.Truncated = output.text()
	result.Output = redactSetupOutput(result.Output, env)
	if len(result.Output) > 64<<10 {
		result.Output = strings.ToValidUTF8(result.Output[:64<<10], "?")
		result.Truncated = true
	}
	return result
}

type setupOutput struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (output *setupOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	count := len(data)
	remaining := (64 << 10) - len(output.data)
	if len(data) > remaining {
		output.truncated = true
		data = data[:remaining]
	}
	output.data = append(output.data, data...)
	return count, nil
}
func (output *setupOutput) text() (string, bool) {
	output.mu.Lock()
	defer output.mu.Unlock()
	// Remove terminal control characters; React renders this as ordinary text.
	value := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, strings.ToValidUTF8(string(output.data), "?"))
	return value, output.truncated
}

var setupURLCredentials = regexp.MustCompile(`(?i)(https?|socks[45]h?)://[^/@\s]+@`)

func redactSetupOutput(value string, env []string) string {
	value = setupURLCredentials.ReplaceAllString(value, "$1://[redacted]@")
	for _, entry := range env {
		key, raw, ok := strings.Cut(entry, "=")
		if !ok || !(strings.EqualFold(key, "http_proxy") || strings.EqualFold(key, "https_proxy")) {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.User == nil {
			continue
		}
		if password, ok := parsed.User.Password(); ok && password != "" {
			value = strings.ReplaceAll(value, password, "[redacted]")
			value = strings.ReplaceAll(value, url.QueryEscape(password), "[redacted]")
		}
	}
	return value
}

// SafeSetupOutput applies the same bounds and redaction to displayed and saved
// diagnostics, including results supplied by alternate runners in tests.
func SafeSetupOutput(value string, truncated bool) (string, bool) {
	output := &setupOutput{}
	output.Write([]byte(value))
	value, clipped := output.text()
	value = redactSetupOutput(value, os.Environ())
	if len(value) > domain.MaxSetupOutputBytes {
		value = strings.ToValidUTF8(value[:domain.MaxSetupOutputBytes], "?")
		clipped = true
	}
	return value, truncated || clipped
}
