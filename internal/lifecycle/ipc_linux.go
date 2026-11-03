package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"workspace-manager/internal/storage"
)

func socketDirectory(paths storage.Paths) string {
	return filepath.Join(runtimeDirectory(paths), "controller")
}
func pinnedSocket(paths storage.Paths) (*os.Root, *os.File, string, error) {
	root, err := storage.OpenPrivateDirectory(socketDirectory(paths))
	if err != nil {
		return nil, nil, "", err
	}
	directory, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, nil, "", err
	}
	return root, directory, fmt.Sprintf("/proc/self/fd/%d/control.sock", directory.Fd()), nil
}
func peerUID(conn *net.UnixConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var credential *unix.Ucred
	var inner error
	err = raw.Control(func(fd uintptr) { credential, inner = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
	if err != nil {
		return err
	}
	if inner != nil {
		return inner
	}
	if credential == nil || credential.Uid != uint32(os.Geteuid()) {
		return errors.New("peer UID mismatch")
	}
	return nil
}

// Serve accepts only same-UID connections on a private Unix socket. The pinned
// directory makes long XDG paths safe without Linux's socket pathname limit.
func Serve(ctx context.Context, controller *Controller) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	root, directory, address, err := pinnedSocket(controller.paths)
	if err != nil {
		return err
	}
	defer root.Close()
	defer directory.Close()
	lease, err := storage.TryDirectoryLock(root)
	if errors.Is(err, storage.ErrLocked) {
		return nil
	}
	if err != nil {
		return err
	}
	defer lease.Close()
	if info, e := root.Lstat("control.sock"); e == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("unexpected controller socket entry")
		}
		if err = root.Remove("control.sock"); err != nil {
			return err
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: address, Net: "unix"})
	if err != nil {
		return err
	}
	listener.SetUnlinkOnClose(false)
	defer listener.Close()
	defer root.Remove("control.sock")
	if err = root.Chmod("control.sock", 0600); err != nil {
		return err
	}
	go func() { <-ctx.Done(); listener.Close() }()
	var requests sync.WaitGroup
	defer requests.Wait()
	slots := make(chan struct{}, 16)
	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			conn.Close()
			return nil
		default:
			conn.Close()
			continue
		}
		requests.Add(1)
		go func() {
			defer requests.Done()
			defer func() { <-slots }()
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
			if peerUID(conn) != nil {
				return
			}
			decoder := json.NewDecoder(io.LimitReader(conn, 1<<20))
			decoder.DisallowUnknownFields()
			var request Request
			if decoder.Decode(&request) != nil {
				return
			}
			// One JSON value per connection; the client half-closes its write side.
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				return
			}
			operationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			response := controller.Handle(operationCtx, request)
			_ = json.NewEncoder(conn).Encode(response)
			if request.Action == "stop_controller" && response.Code == "" {
				stop()
			}
		}()
	}
}

func callController(ctx context.Context, paths storage.Paths, request Request) (Response, error) {
	root, directory, address, err := pinnedSocket(paths)
	if err != nil {
		return Response{}, err
	}
	defer root.Close()
	defer directory.Close()
	conn, err := (&net.Dialer{Timeout: 500 * time.Millisecond}).DialContext(ctx, "unix", address)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	socket := conn.(*net.UnixConn)
	if err := peerUID(socket); err != nil {
		return Response{}, err
	}
	deadline := time.Now().Add(20 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return Response{}, err
	}
	if err := socket.CloseWrite(); err != nil {
		return Response{}, err
	}
	var response Response
	decoder := json.NewDecoder(io.LimitReader(conn, 24<<20))
	if err := decoder.Decode(&response); err != nil {
		return Response{}, err
	}
	return response, nil
}

// Service is a reconnectable GUI facade. It starts no project command itself.
// Transport failures never retry a mutation; callers inspect its token outcome.
type Service struct{ mu sync.Mutex }

func NewService() *Service { return &Service{} }
func (s *Service) Execute(ctx context.Context, request Request) Response {
	paths, err := storage.ResolvePaths()
	if err != nil {
		return failure("XDG storage paths are unavailable.")
	}
	if err = s.ensure(ctx, paths); err != nil {
		return failure("The local controller could not start. Check Diagnostics and private XDG directory permissions.")
	}
	response, err := callController(ctx, paths, request)
	if err != nil {
		return Response{Code: "connection_lost", Message: "Controller response was lost. Refresh Activity and query the review outcome before retrying; the action may already have been accepted."}
	}
	return response
}
func (s *Service) ensure(ctx context.Context, paths storage.Paths) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := callController(ctx, paths, Request{Action: "outcome"}); err == nil {
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, "--controller")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.Env = os.Environ()
	if err = command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("controller startup timed out")
		case <-ticker.C:
			if _, err := callController(ctx, paths, Request{Action: "outcome"}); err == nil {
				return nil
			}
		}
	}
}
