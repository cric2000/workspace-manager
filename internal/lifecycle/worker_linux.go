// Package lifecycle owns the independent controller and durable managed runs.
package lifecycle

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"golang.org/x/sys/unix"
	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

func runtimeDirectory(paths storage.Paths) string { return filepath.Join(paths.StateDir, "runtime") }
func runDirectory(paths storage.Paths) string     { return filepath.Join(runtimeDirectory(paths), "runs") }
func openRuns(paths storage.Paths) (*storage.Repository[domain.ManagedRun], error) {
	codec, _ := storage.NewCodec[domain.ManagedRun](1, nil)
	return storage.OpenRepository(runDirectory(paths), codec)
}
func runLock(paths storage.Paths, run domain.ManagedRun) (*os.File, error) {
	hash := sha256.Sum256([]byte(executionIdentity(run.Command.Configuration)))
	root, err := storage.OpenPrivateDirectory(filepath.Join(runtimeDirectory(paths), "owners", hex.EncodeToString(hash[:])))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return storage.TryDirectoryLock(root)
}

func processStart(pid int) string {
	if pid <= 0 {
		return ""
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	_, tail, ok := strings.Cut(string(data), ") ")
	if !ok {
		return ""
	}
	fields := strings.Fields(tail)
	if len(fields) < 20 || fields[0] == "Z" {
		return ""
	}
	return fields[19]
}
func bootID() string {
	data, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(data))
}

// pidfd plus start time and boot ID prevents signals to recycled PIDs. Only
// workers created by this application are addressed, never process names.
func signalWorker(run domain.ManagedRun, force bool) error {
	if run.BootID != bootID() || run.WorkerStart == "" || processStart(run.WorkerPID) != run.WorkerStart {
		return errors.New("worker identity unavailable; no signal sent")
	}
	fd, err := unix.PidfdOpen(run.WorkerPID, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if processStart(run.WorkerPID) != run.WorkerStart {
		return errors.New("worker changed")
	}
	sig := unix.SIGTERM
	if force {
		sig = unix.SIGUSR1
	}
	return unix.PidfdSendSignal(fd, sig, nil, 0)
}

type outputBuffer struct {
	mu        sync.Mutex
	data      string
	truncated bool
	secrets   []string
}

var credentialPattern = regexp.MustCompile(`(?i)(authorization[=: ]+|(?:api[_-]?key|token|password|secret)[=: ]+)[^\s]+`)
var urlCredential = regexp.MustCompile(`(https?://)[^/@\s]+:[^/@\s]+@`)

func (b *outputBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data += string(data)
	if len(b.data) > 64<<10 {
		b.data = b.data[len(b.data)-(64<<10):]
		if position := strings.IndexByte(b.data, '\n'); position >= 0 {
			b.data = b.data[position+1:]
		} else {
			b.data = ""
		}
		b.truncated = true
	}
	return len(data), nil
}
func (b *outputBuffer) snapshot(flush ...bool) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data := b.data
	if len(flush) == 0 || !flush[0] {
		if end := strings.LastIndexByte(data, '\n'); end >= 0 {
			data = data[:end+1]
		} else {
			data = ""
		}
	}
	value := strings.ToValidUTF8(data, "�")
	for _, secret := range b.secrets {
		if len(secret) >= 4 {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	value = credentialPattern.ReplaceAllString(value, "${1}[redacted]")
	value = urlCredential.ReplaceAllString(value, "${1}[redacted]@")
	value = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) && c != '\n' && c != '\t' {
			return -1
		}
		return c
	}, value)
	return value, b.truncated
}

var environmentKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func commandEnvironment(configuration domain.CommandConfiguration) ([]string, []string, error) {
	env := map[string]string{}
	for _, key := range []string{"HOME", "USER", "LOGNAME", "PATH", "LANG", "LC_ALL", "DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "XDG_CURRENT_DESKTOP", "XDG_SESSION_TYPE", "TERM"} {
		if v, ok := os.LookupEnv(key); ok {
			env[key] = v
		}
	}
	if env["PATH"] == "" {
		env["PATH"] = "/usr/local/bin:/usr/bin:/bin"
	}
	var secrets []string
	if configuration.Runtime != nil && len(configuration.Runtime.EnvironmentFiles) > 0 {
		root, err := os.OpenRoot(configuration.WorkingDirectory)
		if err != nil {
			return nil, nil, err
		}
		defer root.Close()
		dir, err := root.Open(".")
		if err != nil {
			return nil, nil, err
		}
		defer dir.Close()
		for _, path := range configuration.Runtime.EnvironmentFiles {
			fd, err := unix.Openat2(int(dir.Fd()), path, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
			if err != nil {
				return nil, nil, errors.New("environment file unavailable")
			}
			f := os.NewFile(uintptr(fd), path)
			info, err := f.Stat()
			if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
				f.Close()
				return nil, nil, errors.New("environment file must be a regular file up to 64 KiB")
			}
			scanner := bufio.NewScanner(io.LimitReader(f, (64<<10)+1))
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				k, v, ok := strings.Cut(line, "=")
				k = strings.TrimSpace(k)
				v = strings.TrimSpace(v)
				if !ok || !environmentKey.MatchString(k) || strings.ContainsRune(v, 0) {
					f.Close()
					return nil, nil, errors.New("environment files require literal KEY=VALUE lines")
				}
				if len(v) >= 2 && (v[0] == '\'' && v[len(v)-1] == '\'' || v[0] == '"' && v[len(v)-1] == '"') {
					v = v[1 : len(v)-1]
				}
				env[k] = v
				secrets = append(secrets, v)
			}
			err = scanner.Err()
			f.Close()
			if err != nil {
				return nil, nil, errors.New("environment file could not be read")
			}
		}
	}
	result := make([]string, 0, len(env))
	for k, v := range env {
		result = append(result, k+"="+v)
	}
	slices.Sort(result)
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	return result, secrets, nil
}

// CheckReadiness never follows redirects or uses proxies. DNS is pinned to
// loopback, including localhost, and each request is bounded independently.
func CheckReadiness(ctx context.Context, check domain.ReadinessCheck) error {
	if err := check.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(check.TimeoutSeconds)*time.Second)
	defer cancel()
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(host, "localhost") {
			host = "127.0.0.1"
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("non-loopback readiness refused")
		}
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, net.JoinHostPort(host, port))
	}
	transport := &http.Transport{DialContext: dial, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if check.Kind == "tcp" {
			conn, err := dial(ctx, "tcp", net.JoinHostPort(check.Host, strconv.Itoa(check.Port)))
			if err == nil {
				conn.Close()
				return nil
			}
		} else {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, check.URL, nil)
			res, err := client.Do(req)
			if err == nil {
				res.Body.Close()
				if res.StatusCode == check.ExpectedStatus {
					return nil
				}
			}
		}
		timer := time.NewTimer(time.Duration(check.IntervalSeconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// RunWorker runs outside the GUI and controller lifetimes. It is the sole owner
// of its child's process group, logs and durable status; controller restart only
// reconnects to metadata and signals this authenticated worker identity.
func RunWorker(ctx context.Context, paths storage.Paths, id domain.ID, reviewAction func(context.Context, domain.CommandAction) (string, error)) error {
	if id.Validate() != nil {
		return errors.New("invalid run ID")
	}
	repo, err := openRuns(paths)
	if err != nil {
		return err
	}
	defer repo.Close()
	snapshot, err := repo.Get(ctx, id)
	if err != nil {
		return err
	}
	run := snapshot.Value
	lease, err := runLock(paths, run)
	if err != nil {
		return err
	}
	defer lease.Close()
	snapshot, err = repo.Get(ctx, id)
	if err != nil {
		return err
	}
	run = snapshot.Value
	if run.State != "pending" {
		return errors.New("run is no longer pending")
	}
	run.WorkerPID = os.Getpid()
	run.WorkerStart = processStart(run.WorkerPID)
	run.BootID = bootID()
	run.State = "starting"
	save := func() error {
		run.UpdatedAt = time.Now().UTC()
		err := storage.UpdateRuntime(context.Background(), repo, run, snapshot.Revision)
		if err != nil {
			return err
		}
		snapshot, err = repo.Get(context.Background(), id)
		return err
	}
	if err = save(); err != nil {
		return err
	}
	fail := func(message string) error { run.State = "failed"; run.Message = message; return save() }
	configuration := run.Command.Configuration
	action := domain.CommandAction{Executable: configuration.Executable, Arguments: configuration.Arguments, WorkingDirectory: configuration.WorkingDirectory}
	identity, err := reviewAction(ctx, action)
	if err != nil || identity != run.ExecutableIdentity {
		return fail("Executable or working directory changed; review and retry.")
	}
	env, secrets, err := commandEnvironment(configuration)
	if err != nil {
		return fail("Environment files are unavailable or invalid. Use literal KEY=VALUE lines; values are never saved.")
	}
	output := &outputBuffer{secrets: secrets}
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1)
	defer signal.Stop(signals)
	if configuration.Runtime != nil && configuration.Runtime.DelaySeconds > 0 {
		timer := time.NewTimer(time.Duration(configuration.Runtime.DelaySeconds) * time.Second)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			run.State = "stopped"
			return save()
		case <-signals:
			timer.Stop()
			run.State = "stopped"
			return save()
		}
	}
	command := exec.Command(configuration.Executable, configuration.Arguments...)
	identity, err = reviewAction(ctx, action)
	if err != nil || identity != run.ExecutableIdentity {
		return fail("Executable or working directory changed during startup delay. Review again.")
	}
	command.Dir = configuration.WorkingDirectory
	command.Env = env
	command.Stdout = output
	command.Stderr = output
	command.WaitDelay = 2 * time.Second
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = command.Start(); err != nil {
		return fail("Command could not start. Check its executable and directory.")
	}
	run.PID = command.Process.Pid
	run.ProcessStart = processStart(run.PID)
	run.State = "running"
	done, signalGroup := watchOwned(command)
	readCtx, cancelRead := context.WithCancel(context.Background())
	defer cancelRead()
	ready := make(chan error, 1)
	if configuration.Runtime != nil && configuration.Runtime.Readiness != nil {
		go func() { ready <- CheckReadiness(readCtx, *configuration.Runtime.Readiness) }()
	} else {
		ready = nil
	}
	if err = save(); err != nil {
		run.State = "failed"
		run.Message = "Runtime status could not be saved. The worker retains ownership; stop it explicitly and inspect storage."
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var deadline <-chan time.Time
	var stopTimer *time.Timer
	defer func() {
		if stopTimer != nil {
			stopTimer.Stop()
		}
	}()
	stopping := false
	var stopCancel context.CancelFunc
	var stopDone chan struct{}
	defer func() {
		if stopCancel != nil {
			stopCancel()
			<-stopDone
		}
	}()
	contextDone := ctx.Done()
	for {
		select {
		case err := <-done:
			cancelRead()
			if stopCancel != nil {
				stopCancel()
				<-stopDone
				stopCancel = nil
			}
			run.Output, run.Truncated = output.snapshot(true)
			if command.ProcessState == nil {
				run.State = "unknown"
				run.Message = "Process exit could not be verified. Inspect remaining descendants before retrying."
				return save()
			}
			run.ExitCode = command.ProcessState.ExitCode()
			if stopping || err == nil {
				run.State = "stopped"
				run.Message = "Command exited."
			} else {
				run.State = "failed"
				run.Message = fmt.Sprintf("Command exited with code %d. Inspect output before retrying.", run.ExitCode)
			}
			// Descendants which intentionally detached cannot be owned by PID name.
			if unix.Kill(-run.PID, 0) == nil {
				run.State = "unknown"
				run.Message = "The leader exited while descendants may remain. Inspect them before retrying; no automatic group adoption."
			}
			return save()
		case err := <-ready:
			ready = nil
			if !stopping {
				if err == nil {
					run.State = "ready"
					run.Message = "Readiness check passed."
				} else {
					run.State = "failed"
					run.Message = "Readiness timed out; the process is still owned. Stop it or inspect logs."
				}
			}
			_ = save()
		case sig := <-signals:
			if sig == syscall.SIGUSR1 {
				if err := signalGroup(unix.SIGKILL); err != nil {
					run.Message = "Force stop refused: process identity is unavailable."
				} else {
					stopping = true
					run.State = "stopping"
					run.Message = "Explicit force stop requested."
				}
				_ = save()
				continue
			}
			if stopping {
				continue
			}
			stopping = true
			cancelRead()
			run.State = "stopping"
			run.Message = "Waiting for graceful stop. Force stop requires a separate explicit action."
			grace := 10
			term := unix.SIGTERM
			if configuration.Runtime != nil && configuration.Runtime.Stop != nil {
				policy := configuration.Runtime.Stop
				grace = policy.GraceSeconds
				if policy.Signal == "SIGINT" {
					term = unix.SIGINT
				}
				if policy.Action != nil {
					current, e := reviewAction(context.Background(), *policy.Action)
					if e != nil || current != run.StopIdentity {
						run.Message = "Stop action changed after launch review; it was skipped. Sending only the approved graceful signal."
					} else {
						stopCtx, cancel := context.WithTimeout(context.Background(), time.Duration(grace)*time.Second)
						stopCancel = cancel
						stopDone = make(chan struct{})
						go func() { defer close(stopDone); runStopAction(stopCtx, *policy.Action, env, output) }()
					}
				}
			}
			if err := signalGroup(term); err != nil {
				run.Message = "Graceful signal could not be sent; process identity is unavailable."
			}
			stopTimer = time.NewTimer(time.Duration(grace) * time.Second)
			deadline = stopTimer.C
			_ = save()
		case <-deadline:
			deadline = nil
			run.State = "failed"
			run.Message = "Graceful stop timed out. The process is still owned; force stop requires explicit confirmation."
			_ = save()
		case <-ticker.C:
			run.Output, run.Truncated = output.snapshot()
			if err := save(); err != nil {
				run.State = "failed"
				run.Message = "Runtime status could not be saved. The worker retains ownership; stop it explicitly and inspect storage."
			}
		case <-contextDone:
			contextDone = nil
			select {
			case signals <- syscall.SIGTERM:
			default:
			}
		}
	}
}
