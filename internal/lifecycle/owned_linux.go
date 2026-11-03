package lifecycle

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"workspace-manager/internal/domain"
)

// watchOwned waits without reaping, then serializes the final reap with group
// signals. While a signal holds the lock the original leader cannot be reaped,
// so its PID/PGID cannot be recycled between validation and kill(-pgid).
// This function must be the only caller of Wait for this already-started child.
func watchOwned(command *exec.Cmd) (<-chan error, func(unix.Signal) error) {
	var mu sync.Mutex
	reaped := false
	pid := command.Process.Pid
	done := make(chan error, 1)
	signal := func(sig unix.Signal) error {
		mu.Lock()
		defer mu.Unlock()
		if reaped {
			return errors.New("original process was reaped; group signalling refused")
		}
		pgid, err := unix.Getpgid(pid)
		if err != nil || pgid != pid {
			return errors.New("owned process group changed")
		}
		return unix.Kill(-pid, sig)
	}
	go func() {
		var info unix.Siginfo
		var err error
		for {
			err = unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
			if !errors.Is(err, unix.EINTR) {
				break
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			reaped = true
			done <- err
			return
		}
		err = command.Wait()
		reaped = true
		done <- err
	}()
	return done, signal
}

func runStopAction(ctx context.Context, action domain.CommandAction, env []string, output *outputBuffer) {
	command := exec.Command(action.Executable, action.Arguments...)
	command.Dir = action.WorkingDirectory
	command.Env = env
	command.Stdout = output
	command.Stderr = output
	command.WaitDelay = time.Second
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if command.Start() != nil {
		return
	}
	done, signal := watchOwned(command)
	select {
	case <-done:
		return
	case <-ctx.Done():
		_ = signal(unix.SIGKILL)
		<-done
	}
}
