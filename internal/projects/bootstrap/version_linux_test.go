package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workspace-manager/internal/domain"
)

func TestVersionRunnerIsolatesConfigurationAndBoundsOutput(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	script := filepath.Join(t.TempDir(), "probe")
	body := "#!/bin/sh\n[ \"$1\" = --version ] || exit 2\n[ -z \"$NODE_OPTIONS$PYTHONPATH$GIT_DIR$CUSTOM_SECRET\" ] || exit 3\n[ \"$GOTOOLCHAIN\" = local ] || exit 4\n[ \"$COREPACK_ENABLE_NETWORK\" = 0 ] || exit 5\n[ \"$GOENV\" = off ] || exit 6\n[ -d \"$HOME\" ] || exit 7\nprintf '%s' \"$PWD\" > '" + marker + "'\ni=0\nwhile [ $i -lt 5000 ]; do printf x; i=$((i+1)); done\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"NODE_OPTIONS", "PYTHONPATH", "GIT_DIR", "CUSTOM_SECRET", "GOTOOLCHAIN", "GOENV", "COREPACK_ENABLE_NETWORK"} {
		t.Setenv(key, "must-not-inherit")
	}
	result := RunVersion(context.Background(), domain.CommandAction{Executable: script, Arguments: []string{"--version"}, WorkingDirectory: "/not/a/project"}, "")
	if result.Err != nil || result.ExitCode != 0 || !result.Truncated || len(result.Output) != 4096 {
		t.Fatal(result)
	}
	data, err := os.ReadFile(marker)
	if err != nil || !strings.HasPrefix(string(data), filepath.Join(os.TempDir(), "workspace-manager-version-")) {
		t.Fatal(string(data), err)
	}
	if _, err := os.Stat(string(data)); !os.IsNotExist(err) {
		t.Fatal("probe cache retained", err)
	}
}

func TestVersionRunnerTimeoutKillsOwnedProcessGroup(t *testing.T) {
	script := filepath.Join(t.TempDir(), "probe")
	marker := filepath.Join(t.TempDir(), "child-finished")
	body := "#!/bin/sh\n(/bin/sleep 1; printf unsafe > '" + marker + "') &\nwait\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	result := RunVersion(ctx, domain.CommandAction{Executable: script}, "")
	if !errors.Is(result.Err, context.DeadlineExceeded) || !result.Started || time.Since(start) > time.Second {
		t.Fatal(result)
	}
	// An independent wait verifies the owned descendant cannot finish later.
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("probe left its child running", err)
	}
}
