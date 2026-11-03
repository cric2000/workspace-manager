//go:build linux && cgo

// Package linux adapts Linux desktop facilities. GIO owns desktop-entry syntax,
// XDG search paths/precedence, locale selection and visibility semantics.
package linux

/*
#cgo pkg-config: gio-unix-2.0
#include <gio/gdesktopappinfo.h>

static GDesktopAppInfo *wm_desktop_info(gpointer info) {
    return G_IS_DESKTOP_APP_INFO(info) ? G_DESKTOP_APP_INFO(info) : NULL;
}

static void wm_free_apps(GList *apps) {
    g_list_free_full(apps, g_object_unref);
}

static gboolean wm_terminal(GDesktopAppInfo *info) {
    return g_desktop_app_info_get_boolean(info, "Terminal");
}

static gboolean wm_dbus_activatable(GDesktopAppInfo *info) {
    return g_desktop_app_info_get_boolean(info, "DBusActivatable");
}
*/
import "C"

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/unix"
	"workspace-manager/internal/applications"
)

// DesktopCatalog uses the process's login environment. Do not change XDG/locale
// variables after GIO starts: GLib caches them. It needs neither GTK initialization
// nor a display connection. Launching delegates desktop-entry semantics to GIO.
type DesktopCatalog struct{}

// Discover returns menu-visible desktop applications sorted by name and ID.
// Hidden overrides, malformed entries and failed TryExec checks are excluded by
// GIO; should_show additionally applies NoDisplay/OnlyShowIn/NotShowIn.
// GIO enumeration itself is synchronous: cancellation is checked before/after it
// and during conversion. Cancelled results never contain a partial catalogue.
func (DesktopCatalog) Discover(ctx context.Context) ([]applications.DesktopEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	list := C.g_app_info_get_all()
	defer C.wm_free_apps(list)
	entries := make([]applications.DesktopEntry, 0)
	for node := list; node != nil; node = node.next {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info := C.wm_desktop_info(node.data)
		if info == nil {
			continue
		}
		entry, valid := desktopEntry(info)
		if valid && entry.Visible {
			entries = append(entries, entry)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b applications.DesktopEntry) int {
		if order := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return entries, nil
}

// Lookup resolves from the same registration list as Discover, including
// non-menu registrations (Visible=false). g_desktop_app_info_new(id) is not used:
// it can fall back to a system entry when a minimal user Hidden tombstone fails
// construction. The registered list correctly masks that ID instead.
// Success means registered metadata, not proven launchability or running status.
func (DesktopCatalog) Lookup(ctx context.Context, id string) (applications.DesktopEntry, error) {
	var entry applications.DesktopEntry
	err := withDesktopInfo(ctx, id, func(_ *C.GDesktopAppInfo, current applications.DesktopEntry) error {
		entry = current
		return nil
	})
	return entry, err
}

// Keep the exact registered GObject alive until the operation completes. A new
// constructor by ID could bypass a Hidden override or select a different entry.
func withDesktopInfo(ctx context.Context, id string, operation func(*C.GDesktopAppInfo, applications.DesktopEntry) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := applications.ValidateDesktopID(id); err != nil {
		return err
	}
	list := C.g_app_info_get_all()
	defer C.wm_free_apps(list)
	for node := list; node != nil; node = node.next {
		if err := ctx.Err(); err != nil {
			return err
		}
		info := C.wm_desktop_info(node.data)
		if info == nil {
			continue
		}
		app := (*C.GAppInfo)(unsafe.Pointer(info))
		if C.GoString(C.g_app_info_get_id(app)) != id {
			continue
		}
		entry, valid := desktopEntry(info)
		if err := ctx.Err(); err != nil {
			return err
		}
		if valid {
			return operation(info, entry)
		}
		break
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return applications.ErrUnavailable
}

func (DesktopCatalog) Review(ctx context.Context, id string) (applications.LaunchSpec, error) {
	var spec applications.LaunchSpec
	err := withDesktopInfo(ctx, id, func(_ *C.GDesktopAppInfo, entry applications.DesktopEntry) error {
		if !entry.Visible {
			return applications.ErrUnavailable
		}
		fingerprint, err := launchFingerprint(entry)
		if err != nil {
			return err
		}
		spec = applications.LaunchSpec{Entry: entry, Fingerprint: fingerprint}
		return ctx.Err()
	})
	return spec, err
}

func (DesktopCatalog) Launch(ctx context.Context, reviewed applications.LaunchSpec) error {
	return withDesktopInfo(ctx, reviewed.Entry.ID, func(info *C.GDesktopAppInfo, entry applications.DesktopEntry) error {
		if !entry.Visible {
			return applications.ErrUnavailable
		}
		fingerprint, err := launchFingerprint(entry)
		if err != nil {
			return applications.ErrUnavailable
		}
		if fingerprint != reviewed.Fingerprint {
			return applications.ErrLaunchChanged
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// GIO handles quoting, field codes, Path, Terminal and D-Bus activation.
		// This synchronous desktop handoff runs on the calling Go worker, never
		// the GTK thread. Once dispatched, cancellation must not imply no launch.
		// No GDK activation token is available from the WebView click; desktop
		// focus policy decides whether the resulting window receives focus.
		launchContext := C.g_app_launch_context_new()
		defer C.g_object_unref(C.gpointer(launchContext))
		var launchError *C.GError
		ok := C.g_app_info_launch((*C.GAppInfo)(unsafe.Pointer(info)), nil, launchContext, &launchError)
		if launchError != nil {
			C.g_error_free(launchError)
		}
		if ok == 0 {
			return errors.New("desktop launch failed")
		}
		return nil
	})
}

// Hash every byte, including fields not displayed (such as desktop extensions),
// and the effective GIO metadata. Bound reads and reject FIFOs/devices. Normal
// desktop-file symlinks are supported. This detects edits; it is not a sandbox
// against a user concurrently replacing installed executables or registrations.
func launchFingerprint(entry applications.DesktopEntry) (string, error) {
	fd, err := unix.Open(entry.DesktopFile, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(fd), entry.DesktopFile)
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !stat.Mode().IsRegular() || stat.Size() > 1<<20 {
		return "", applications.ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return "", err
	}
	if len(data) > 1<<20 {
		return "", applications.ErrUnavailable
	}
	metadata, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	hash.Write(metadata)
	hash.Write([]byte{0})
	hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// The GObject owns borrowed strings. All Go strings are copied before unref;
// g_icon_to_string allocates its result, which must be freed separately.
func desktopEntry(info *C.GDesktopAppInfo) (applications.DesktopEntry, bool) {
	if C.g_desktop_app_info_get_is_hidden(info) != 0 {
		return applications.DesktopEntry{}, false
	}
	app := (*C.GAppInfo)(unsafe.Pointer(info))
	entry := applications.DesktopEntry{
		ID:              C.GoString(C.g_app_info_get_id(app)),
		Name:            C.GoString(C.g_app_info_get_display_name(app)),
		GenericName:     C.GoString(C.g_desktop_app_info_get_generic_name(info)),
		Description:     C.GoString(C.g_app_info_get_description(app)),
		DesktopFile:     C.GoString(C.g_desktop_app_info_get_filename(info)),
		Commandline:     C.GoString(C.g_app_info_get_commandline(app)),
		Executable:      C.GoString(C.g_app_info_get_executable(app)),
		Terminal:        C.wm_terminal(info) != 0,
		DBusActivatable: C.wm_dbus_activatable(info) != 0,
		Visible:         C.g_app_info_should_show(app) != 0,
	}
	pathKey := C.CString("Path")
	workingDirectory := C.g_desktop_app_info_get_string(info, pathKey)
	entry.WorkingDirectory = C.GoString(workingDirectory)
	C.g_free(C.gpointer(workingDirectory))
	C.g_free(C.gpointer(pathKey))
	if icon := C.g_app_info_get_icon(app); icon != nil {
		serialized := C.g_icon_to_string(icon)
		entry.Icon = C.GoString(serialized)
		C.g_free(C.gpointer(serialized))
	}
	if applications.ValidateDesktopID(entry.ID) != nil || strings.TrimSpace(entry.Name) == "" || !filepath.IsAbs(entry.DesktopFile) {
		return applications.DesktopEntry{}, false
	}
	for _, value := range []string{entry.Name, entry.GenericName, entry.Description, entry.DesktopFile, entry.Commandline, entry.Executable, entry.Icon, entry.WorkingDirectory} {
		if !utf8.ValidString(value) {
			return applications.DesktopEntry{}, false
		}
	}
	return entry, true
}

// Read sandbox identities through GIO's desktop parser; filenames and executable
// names are insufficient to distinguish Snap apps and parallel instances.
func desktopSnapIdentity(ctx context.Context, id string) (string, string) {
	var instance, app string
	_ = withDesktopInfo(ctx, id, func(info *C.GDesktopAppInfo, _ applications.DesktopEntry) error {
		get := func(key string) string {
			field := C.CString(key)
			defer C.free(unsafe.Pointer(field))
			value := C.g_desktop_app_info_get_string(info, field)
			if value == nil {
				return ""
			}
			defer C.g_free(C.gpointer(value))
			return C.GoString(value)
		}
		instance, app = get("X-SnapInstanceName"), get("X-SnapAppName")
		return nil
	})
	return instance, app
}
