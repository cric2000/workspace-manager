package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

const autostartFile = "io.github.workspace_manager.Autostart.desktop"
const autostartHeader = "# Workspace Manager owned login entry v1\n"
const loginGrantID domain.ID = "00000000000000000000000000000001"

// DesktopExec quotes one literal argv element using both Desktop Entry escaping
// layers. Percent signs are doubled so paths cannot introduce field codes.
func DesktopExec(value string) (string, error) {
	if value == "" || strings.ContainsAny(value, "\x00\r\n\t") {
		return "", errors.New("invalid desktop executable")
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range value {
		switch c {
		case '%':
			b.WriteString("%%")
		case '\\':
			b.WriteString("\\\\\\\\")
		case '"', '`', '$':
			b.WriteString("\\\\")
			b.WriteRune(c)
		default:
			b.WriteRune(c)
		}
	}
	b.WriteByte('"')
	return b.String(), nil
}
func openAutostart(paths storage.Paths) (*os.Root, error) {
	base := filepath.Dir(paths.ConfigDir)
	// Provision the app directory first without relaxing shared XDG permissions.
	private, err := storage.OpenPrivateDirectory(paths.ConfigDir)
	if err != nil {
		return nil, err
	}
	private.Close()
	fd, err := unix.Openat2(unix.AT_FDCWD, base, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	if err = unix.Mkdirat(fd, "autostart", 0700); err != nil && !errors.Is(err, unix.EEXIST) {
		return nil, err
	}
	child, err := unix.Openat(fd, "autostart", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(child)
	var stat unix.Stat_t
	if err = unix.Fstat(child, &stat); err != nil {
		return nil, err
	}
	if stat.Uid != uint32(os.Geteuid()) || stat.Mode&0022 != 0 {
		return nil, errors.New("unsafe autostart directory")
	}
	return os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", child))
}
func writeAutostart(paths storage.Paths, executable string, enabled bool) error {
	quoted, err := DesktopExec(executable)
	if err != nil || !filepath.IsAbs(executable) {
		return errors.New("autostart requires an absolute executable")
	}
	root, err := openAutostart(paths)
	if err != nil {
		return err
	}
	defer root.Close()
	if info, e := root.Lstat(autostartFile); e == nil {
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0022 != 0 || !info.Mode().IsRegular() || info.Size() > 16<<10 {
			return errors.New("existing autostart entry is not owned")
		}
		f, e := root.OpenFile(autostartFile, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if e != nil {
			return e
		}
		data, e := io.ReadAll(io.LimitReader(f, 16<<10))
		f.Close()
		if e != nil || !strings.HasPrefix(string(data), autostartHeader) {
			return errors.New("existing autostart entry is not owned")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	hidden := "true"
	if enabled {
		hidden = "false"
	}
	data := autostartHeader + "[Desktop Entry]\nType=Application\nName=Workspace Manager\nComment=Restore your reviewed developer workspace after login\nExec=" + quoted + " --login\nTerminal=false\nHidden=" + hidden + "\nX-GNOME-Autostart-enabled=" + fmt.Sprint(enabled) + "\n"
	name := ".workspace-manager-" + fmt.Sprint(time.Now().UnixNano())
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(name)
	if _, err = f.WriteString(data); err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = root.Rename(name, autostartFile); err != nil {
		return err
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (c *Controller) enableLogin(ctx context.Context, held *heldPlan) Response {
	settings := held.request.Settings
	if settings == nil || !settings.Autostart {
		return failure("Enable login startup in Settings and review it again.")
	}
	for _, task := range held.plan.Tasks {
		if task.Blocked != "" {
			return failure("Login startup cannot be enabled while its review has unavailable or unapproved tasks.")
		}
	}
	held.started = true
	err := storage.WithDirectoryLock(ctx, c.operations, func() error {
		current, err := c.settings.Get(ctx)
		if err != nil {
			return err
		}
		if current.Revision != held.request.Revision {
			return storage.ErrConflict
		}
		if err := writeAutostart(c.paths, c.executable, false); err != nil {
			return err
		}
		now := time.Now().UTC()
		grant := domain.StartupAuthorization{ID: loginGrantID, Fingerprint: held.plan.Fingerprint, ApprovedAt: now}
		old, err := c.grants.Get(ctx, loginGrantID)
		if os.IsNotExist(err) {
			err = c.grants.Create(ctx, grant)
		} else if err == nil {
			err = c.grants.Update(ctx, loginGrantID, old.Revision, func(domain.StartupAuthorization) (domain.StartupAuthorization, error) { return grant, nil })
		}
		if err != nil {
			return err
		}
		if err = c.settings.Update(ctx, held.request.Revision, func(domain.Settings) (domain.Settings, error) { return *settings, nil }); err != nil {
			return err
		}
		return writeAutostart(c.paths, c.executable, true)
	})
	if err != nil {
		held.result = failure("Login startup was not fully enabled. Reload Settings before trying again; existing files were preserved when ownership could not be verified.")
		return held.result
	}
	held.result = Response{Message: "Login startup enabled for this exact reviewed configuration. Future configuration changes require another review."}
	return held.result
}
func (c *Controller) login(ctx context.Context) Response {
	settings, err := c.getSettings(ctx)
	if err != nil {
		return failure("Login settings are unavailable.")
	}
	if !settings.Value.Autostart {
		return Response{Message: "Login startup is disabled."}
	}
	req := Request{Action: "review_login", Settings: &settings.Value, Revision: settings.Revision}
	review := c.review(ctx, req)
	if review.Plan == nil {
		return review
	}
	grant, err := c.grants.Get(ctx, loginGrantID)
	if err != nil || grant.Value.Fingerprint != review.Plan.Fingerprint {
		return failure("Login configuration changed or has no explicit approval. Open Settings and review it again; no applications were launched.")
	}
	// Persist before dispatch. Repeated autostart invocations in the same desktop
	// session never replay desktop handoffs, even after a controller restart.
	identity := bootID() + "|" + os.Getenv("XDG_SESSION_ID") + "|" + os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	hash := sha256.Sum256([]byte(identity))
	id := domain.ID(hex.EncodeToString(hash[:16]))
	marker := domain.StartupAuthorization{ID: id, Fingerprint: review.Plan.Fingerprint, ApprovedAt: time.Now().UTC()}
	if _, err := c.grants.Get(ctx, id); err == nil {
		return Response{Message: "Login startup has already been dispatched for this desktop session."}
	} else if !os.IsNotExist(err) {
		return failure("Login dispatch history is unavailable; replay refused.")
	}
	if err := c.grants.Create(ctx, marker); err != nil {
		return failure("Login dispatch could not be recorded. Nothing was launched.")
	}
	token := review.Plan.Token
	delay := settings.Value.InitialDelaySeconds
	c.plans[token].loginDispatch = true
	c.plans[token].plan.ExpiresAt = time.Now().Add(time.Duration(delay)*time.Second + 5*time.Minute)
	if delay == 0 {
		return c.commit(ctx, Request{Token: token, SwitchPolicy: "keep"})
	}
	c.workers.Add(1)
	go func() {
		defer c.workers.Done()
		timer := time.NewTimer(time.Duration(delay) * time.Second)
		defer timer.Stop()
		select {
		case <-c.life.Done():
			return
		case <-timer.C:
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if !c.closed {
			c.commit(context.Background(), Request{Token: token, SwitchPolicy: "keep"})
		}
	}()
	return Response{Message: "Reviewed login startup scheduled after its configured delay."}
}
