package lifecycle

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"workspace-manager/internal/domain"
)

func TestOwnedGroupCannotBeSignalledAfterReap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned-exit")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for range 24 {
		command := exec.Command(path)
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		done, signal := watchOwned(command)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if err := signal(unix.SIGTERM); err == nil {
			t.Fatal("accepted group signal after leader reaping")
		}
	}
}
func TestStopActionTimeoutTerminatesOnlyOwnedHelperGroup(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "owned-stop")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'owned-stop-started\\n'\ntrap '' TERM\nwhile :; do sleep 0.1; done\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	output := &outputBuffer{}
	started := time.Now()
	runStopAction(ctx, domain.CommandAction{Executable: path, WorkingDirectory: directory}, []string{"PATH=/usr/bin:/bin"}, output)
	if time.Since(started) > 3*time.Second {
		t.Fatal("stop helper outlived its bounded cancellation")
	}
	text, _ := output.snapshot(true)
	if text != "owned-stop-started\n" {
		t.Fatal(text)
	}
}
