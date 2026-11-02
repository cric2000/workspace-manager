package bootstrap

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	"workspace-manager/internal/domain"
)

var ErrScaffold = errors.New("scaffolder failed or generated an invalid tree")

// PrepareScaffold runs only the reviewed official tool, never generated project
// code. Its workspace is new, private and independent of the destination.
func PrepareScaffold(ctx context.Context, action domain.CommandAction, name string) ([]File, error) {
	return prepareScaffold(ctx, action, name, runScaffolder)
}

func PrepareScaffoldLogged(ctx context.Context, action domain.CommandAction, name string) ([]File, CommandResult, error) {
	command := CommandResult{ExitCode: -1}
	files, err := prepareScaffold(ctx, action, name, func(ctx context.Context, action domain.CommandAction, env []string) error {
		command = runScaffolderLogged(ctx, action, env)
		return command.Err
	})
	return files, command, err
}

func prepareScaffold(ctx context.Context, action domain.CommandAction, name string, run func(context.Context, domain.CommandAction, []string) error) ([]File, error) {
	if action.Validate() != nil || !safeName.MatchString(name) {
		return nil, ErrOptions
	}
	parent, err := OpenParent(filepath.Dir(action.WorkingDirectory))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	base := filepath.Base(action.WorkingDirectory)
	if !strings.HasPrefix(base, "workspace-manager-next-") || !safeName.MatchString(base) {
		return nil, ErrOptions
	}
	parentFD := int(parent.file.Fd())
	if err := unix.Mkdirat(parentFD, base, 0700); err != nil {
		return nil, ErrScaffold
	}
	fd, err := unix.Openat(parentFD, base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrScaffold
	}
	directory := os.NewFile(uintptr(fd), base)
	defer directory.Close()
	var owned unix.Stat_t
	if unix.Fstat(fd, &owned) != nil {
		return nil, ErrScaffold
	}
	root, err := os.OpenRoot("/proc/self/fd/" + strconv.Itoa(fd))
	if err != nil {
		return nil, ErrScaffold
	}
	defer root.Close()
	// Cleanup is rooted in our pinned private directory. Replaced/moved parent
	// entries are never adopted or recursively removed.
	defer func() {
		entries, _ := root.FS().(fs.ReadDirFS).ReadDir(".")
		for _, entry := range entries {
			_ = root.RemoveAll(entry.Name())
		}
		var current unix.Stat_t
		if unix.Fstatat(parentFD, base, &current, unix.AT_SYMLINK_NOFOLLOW) == nil && current.Dev == owned.Dev && current.Ino == owned.Ino {
			_ = unix.Unlinkat(parentFD, base, unix.AT_REMOVEDIR)
		}
	}()
	for _, child := range []string{"home", "config", "cache", "bin"} {
		if root.Mkdir(child, 0700) != nil {
			return nil, ErrScaffold
		}
	}
	// Keep the child's cwd, caches and configuration anchored to the directory
	// descriptor even if its original name is moved or replaced during preparation.
	execution := action
	execution.WorkingDirectory = "/proc/" + strconv.Itoa(os.Getpid()) + "/fd/" + strconv.Itoa(fd)
	// npm exec launches the package's env-node shebang. Keep that nested
	// invocation on the same reviewed interpreter as the initial npm CLI.
	if err := root.Symlink(action.Executable, "bin/node"); err != nil {
		return nil, ErrScaffold
	}
	env := scaffoldEnvironment(execution.WorkingDirectory)
	if err := run(ctx, execution, env); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrScaffold
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var current unix.Stat_t
	if !parent.Current() || unix.Fstatat(parentFD, base, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || current.Dev != owned.Dev || current.Ino != owned.Ino {
		return nil, ErrChanged
	}
	output, err := unix.Openat2(fd, name, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
	if err != nil {
		return nil, ErrScaffold
	}
	generated := os.NewFile(uintptr(output), name)
	defer generated.Close()
	files, err := collectScaffold(generated)
	if err != nil {
		return nil, err
	}
	var manifest struct {
		Name         string
		Scripts      map[string]string
		Dependencies map[string]string
	}
	for _, file := range files {
		if file.Path == "package.json" {
			_ = json.Unmarshal([]byte(file.Contents), &manifest)
		}
	}
	if manifest.Name != name || manifest.Scripts["dev"] != "next dev" || manifest.Dependencies["next"] != NextScaffolderVersion {
		return nil, ErrScaffold
	}
	return files, nil
}

func scaffoldEnvironment(directory string) []string {
	// User npm preferences/credentials and CLI defaults are not imported. Proxy and
	// CA configuration are kept so configured network access can still work.
	env := []string{"PATH=" + filepath.Join(directory, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + filepath.Join(directory, "home"), "XDG_CONFIG_HOME=" + filepath.Join(directory, "config"), "XDG_CACHE_HOME=" + filepath.Join(directory, "cache"), "npm_config_cache=" + filepath.Join(directory, "cache", "npm"), "npm_config_userconfig=" + filepath.Join(directory, "home", ".npmrc"), "npm_config_globalconfig=" + filepath.Join(directory, "config", "npmrc"), "npm_config_registry=https://registry.npmjs.org/", "npm_config_update_notifier=false", "npm_config_audit=false", "npm_config_fund=false", "npm_config_fetch_retries=0", "npm_config_fetch_timeout=60000", "CI=1", "NEXT_TELEMETRY_DISABLED=1", "NO_COLOR=1"}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func runScaffolder(ctx context.Context, action domain.CommandAction, env []string) error {
	return runScaffolderLogged(ctx, action, env).Err
}

func runScaffolderLogged(ctx context.Context, action domain.CommandAction, env []string) CommandResult {
	result := CommandResult{ExitCode: -1}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, action.Executable, action.Arguments...)
	command.Dir, command.Env = action.WorkingDirectory, env
	command.Stdin = nil
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
	result.Output, result.Truncated = SafeSetupOutput(result.Output, result.Truncated)
	return result
}

func collectScaffold(root *os.File) ([]File, error) {
	files := []File{}
	total, entries := 0, 0
	var walk func(*os.File, string) error
	walk = func(directory *os.File, prefix string) error {
		children, err := directory.ReadDir(193)
		if err != nil && !errors.Is(err, io.EOF) {
			return ErrScaffold
		}
		for _, child := range children {
			entries++
			if entries > 192 {
				return ErrScaffold
			}
			relative := prefix + child.Name()
			if !fs.ValidPath(relative) || len(relative) > 1024 || len(strings.Split(relative, "/")) > 8 || child.Name() == ".git" || child.Name() == "node_modules" || child.Name() == ".next" {
				return ErrScaffold
			}
			fd, err := unix.Openat2(int(root.Fd()), relative, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_NONBLOCK | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
			if err != nil {
				return ErrScaffold
			}
			handle := os.NewFile(uintptr(fd), relative)
			info, err := handle.Stat()
			if err != nil {
				handle.Close()
				return ErrScaffold
			}
			if info.IsDir() {
				err = walk(handle, relative+"/")
				handle.Close()
				if err != nil {
					return err
				}
				continue
			}
			if !info.Mode().IsRegular() || info.Size() > 1<<20 || len(files) >= 64 {
				handle.Close()
				return ErrScaffold
			}
			data, err := io.ReadAll(io.LimitReader(handle, (1<<20)+1))
			handle.Close()
			total += len(data)
			if err != nil || total > 1<<20 {
				return ErrScaffold
			}
			file := File{Path: relative, Contents: string(data)}
			if !utf8.Valid(data) || slices.Contains(data, byte(0)) {
				file.Encoding = "base64"
				file.Contents = base64.StdEncoding.EncodeToString(data)
			}
			files = append(files, file)
		}
		return nil
	}
	if err := walk(root, ""); err != nil {
		return nil, err
	}
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	if len(files) == 0 {
		return nil, ErrScaffold
	}
	return files, nil
}
