package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"workspace-manager/internal/domain"
)

func TestSetupRunnerBoundsOutputAndCancelsOwnedChildren(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("shell unavailable")
	}
	directory := t.TempDir()
	root, err := os.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	marker := filepath.Join(directory, "child")
	action := domain.CommandAction{Executable: shell, WorkingDirectory: directory, Arguments: []string{"-c", `printf '%070000d' 0; sleep 60 & child=$!; printf '%s' "$child" > "$1"; wait`, "fixture", marker}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished := make(chan CommandResult, 1)
	go func() { finished <- RunSetup(ctx, action, "", root) }()
	child := 0
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(marker)
		if _, err := fmt.Sscanf(string(data), "%d", &child); err == nil && child > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if child == 0 {
		cancel()
		<-finished
		t.Fatal("helper did not start")
	}
	cancel()
	result := <-finished
	if !result.Started || !errors.Is(result.Err, context.Canceled) || !result.Truncated || len(result.Output) > 64<<10 {
		t.Fatal(result)
	}
	for i := 0; i < 100; i++ {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", child))
		if os.IsNotExist(err) || strings.Contains(string(data), "State:\tZ") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("owned child survived setup cancellation")
}
func TestSetupOutputRemainsBoundedValidUTF8(t *testing.T) {
	output := &setupOutput{}
	if count, err := output.Write([]byte(strings.Repeat("\xff", 100000))); err != nil || count != 100000 {
		t.Fatal(count, err)
	}
	value, truncated := output.text()
	if !truncated || len(value) > 64<<10 || !utf8.ValidString(value) {
		t.Fatal(len(value), truncated)
	}
}

func TestSetupOutputRedactsProxyCredentials(t *testing.T) {
	value := redactSetupOutput("proxy http://alice:private-pass@proxy.invalid:8080 failed: private-pass", []string{"HTTPS_PROXY=http://alice:private-pass@proxy.invalid:8080"})
	if strings.Contains(value, "private-pass") || strings.Contains(value, "alice:") || !strings.Contains(value, "[redacted]") {
		t.Fatal("proxy credentials leaked", value)
	}
}
