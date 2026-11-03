//go:build linux && cgo

package linux

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"workspace-manager/internal/applications"
	"workspace-manager/internal/domain"
)

// Desktop scopes identify applications launched by the desktop, including apps
// without D-Bus activation and Snap launchers. They are not window/readiness
// proofs. Never infer identity from process names, argv or environment contents.
type desktopUnit struct {
	Name        string
	Description string
	LoadState   string
	ActiveState string
	SubState    string
	Following   string
	Path        dbus.ObjectPath
	JobID       uint32
	JobType     string
	JobPath     dbus.ObjectPath
}

func desktopBus(ctx context.Context) (*dbus.Conn, error) {
	if ctx.Err() != nil || os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		return nil, errors.New("session bus unavailable")
	}
	return dbus.ConnectSessionBus()
}

func desktopScopes(ctx context.Context, bus *dbus.Conn) ([]string, error) {
	var units []desktopUnit
	err := bus.Object("org.freedesktop.systemd1", "/org/freedesktop/systemd1").CallWithContext(ctx,
		"org.freedesktop.systemd1.Manager.ListUnitsByPatterns", dbus.FlagNoAutoStart,
		[]string{}, []string{"app-*.scope", "snap.*.scope"}).Store(&units)
	if err != nil {
		return nil, err
	}
	if len(units) > 8192 {
		return nil, errors.New("desktop scope limit exceeded")
	}
	names := make([]string, 0, len(units))
	for _, unit := range units {
		if unit.LoadState == "loaded" && (unit.ActiveState == "active" || unit.ActiveState == "activating" || unit.ActiveState == "reloading") {
			names = append(names, unit.Name)
		}
	}
	return names, nil
}

// systemd escapes hyphens and all bytes outside its unit-name alphabet. Match
// complete desktop identities and a numeric invocation suffix, never substrings.
func unitEscape(value string) string {
	var result strings.Builder
	for i, b := range []byte(value) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == ':' || b == '_' || b == '.' && i != 0 {
			result.WriteByte(b)
		} else {
			const hex = "0123456789abcdef"
			result.WriteString(`\x`)
			result.WriteByte(hex[b>>4])
			result.WriteByte(hex[b&15])
		}
	}
	return result.String()
}
func scopeMatches(unit, desktopID, snapInstance, snapApp string) bool {
	if applications.ValidateDesktopID(desktopID) != nil || !strings.HasSuffix(unit, ".scope") {
		return false
	}
	stem := strings.TrimSuffix(unit, ".scope")
	id := unitEscape(strings.TrimSuffix(desktopID, ".desktop"))
	for _, prefix := range []string{"app-gnome-" + id + "-", "app-" + id + "-", "app-flatpak-" + id + "-"} {
		if suffix, ok := strings.CutPrefix(stem, prefix); ok {
			if n, err := strconv.ParseUint(suffix, 10, 64); err == nil && n > 0 {
				return true
			}
		}
	}
	if snapInstance != "" && snapApp != "" && !strings.ContainsAny(snapInstance+snapApp, "/\x00\n\r.") {
		if suffix, ok := strings.CutPrefix(stem, "snap."+snapInstance+"."+snapApp+"-"); ok && suffix != "" {
			return true
		}
	}
	return false
}
func scopedApplication(ctx context.Context, entry applications.DesktopEntry, scopes []string) applications.InstanceStatus {
	instance, app := desktopSnapIdentity(ctx, entry.ID)
	for _, unit := range scopes {
		if scopeMatches(unit, entry.ID, instance, app) {
			return applications.InstanceStatus{State: "running", Message: "A matching desktop application scope is active. Window readiness is not verified."}
		}
	}
	return applications.InstanceStatus{State: "not_running", Message: "No matching desktop application scope is active. Apps started outside the desktop may not be detected."}
}

func (checker DesktopInstances) CheckDesktop(ctx context.Context, entry applications.DesktopEntry) applications.InstanceStatus {
	if entry.DBusActivatable {
		status := checker.Check(ctx, strings.TrimSuffix(entry.ID, ".desktop"))
		if status.State == "running" {
			return status
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	bus, err := desktopBus(ctx)
	if err == nil {
		defer bus.Close()
		if scopes, err := desktopScopes(ctx, bus); err == nil {
			return scopedApplication(ctx, entry, scopes)
		}
	}
	// Preserve the established identity check on desktops without systemd scopes.
	if entry.DBusActivatable {
		return checker.Check(ctx, strings.TrimSuffix(entry.ID, ".desktop"))
	}
	return applications.InstanceStatus{State: "unknown", Message: "Desktop application scopes are unavailable. Running state cannot be checked."}
}

func recognizedDesktopApplications(ctx context.Context) ([]domain.WorkspaceApplication, []string) {
	warnings := []string{"Capture recognizes desktop application scopes and session D-Bus identities, not arbitrary windows. Apps started outside the desktop, browser tabs and unsaved documents may be missing."}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	bus, err := desktopBus(ctx)
	if err != nil {
		return nil, append(warnings, "Desktop application observation is unavailable.")
	}
	defer bus.Close()
	var names []string
	namesErr := bus.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.ListNames", dbus.FlagNoAutoStart).Store(&names)
	scopes, scopesErr := desktopScopes(ctx, bus)
	if namesErr != nil && scopesErr != nil {
		return nil, append(warnings, "Desktop application observation failed; no running applications were identified.")
	}
	known := map[string]bool{}
	for _, name := range names {
		known[name] = true
	}
	entries, err := (DesktopCatalog{}).Discover(ctx)
	if err != nil {
		return nil, append(warnings, "Application registrations could not be read.")
	}
	var result []domain.WorkspaceApplication
	for _, entry := range entries {
		if entry.ID == "io.github.workspace_manager.WorkspaceManager.desktop" {
			continue
		}
		if ctx.Err() != nil {
			warnings = append(warnings, "Application observation timed out; capture may be incomplete.")
			break
		}
		running := entry.DBusActivatable && domain.ValidBusName(strings.TrimSuffix(entry.ID, ".desktop")) && known[strings.TrimSuffix(entry.ID, ".desktop")]
		if !running && scopesErr == nil {
			running = scopedApplication(ctx, entry, scopes).State == "running"
		}
		if !running {
			continue
		}
		if len(result) >= 64 {
			warnings = append(warnings, "Application capture reached its 64 application limit.")
			break
		}
		result = append(result, domain.WorkspaceApplication{DesktopID: domain.DesktopID(entry.ID), DuplicatePolicy: domain.SkipIfRunning})
	}
	return result, warnings
}
