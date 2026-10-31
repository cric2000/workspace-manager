//go:build linux && cgo

package linux

/*
#cgo pkg-config: gio-2.0
#include <gio/gio.h>
#include <string.h>

static int wm_name_owner(const char *name, GCancellable *cancel) {
    GError *error = NULL;
    // Observe only an existing session address. Never autolaunch a bus, and use
    // a private connection so its failure cannot terminate the GUI through the
    // shared session connection's exit-on-close policy.
    const char *address = g_getenv("DBUS_SESSION_BUS_ADDRESS");
    if (!address || !*address) return -1;
    GDBusConnection *bus = g_dbus_connection_new_for_address_sync(address,
        G_DBUS_CONNECTION_FLAGS_AUTHENTICATION_CLIENT | G_DBUS_CONNECTION_FLAGS_MESSAGE_BUS_CONNECTION,
        NULL, cancel, &error);
    if (!bus) { if (error) g_error_free(error); return -1; }
    g_dbus_connection_set_exit_on_close(bus, FALSE);
    GVariant *result = g_dbus_connection_call_sync(bus, "org.freedesktop.DBus",
        "/org/freedesktop/DBus", "org.freedesktop.DBus", "GetNameOwner",
        g_variant_new("(s)", name), G_VARIANT_TYPE("(s)"),
        G_DBUS_CALL_FLAGS_NO_AUTO_START, 1500, cancel, &error);
    g_dbus_connection_close_sync(bus, NULL, NULL);
    g_object_unref(bus);
    if (!result) {
        int absent = error && g_error_matches(error, G_DBUS_ERROR, G_DBUS_ERROR_NAME_HAS_NO_OWNER);
        if (error) g_error_free(error);
        return absent ? 0 : -1;
    }
    const char *owner = NULL;
    g_variant_get(result, "(&s)", &owner);
    int state = owner && owner[0] == ':' ? 1 : -1;
    g_variant_unref(result);
    return state;
}
*/
import "C"

import (
	"context"
	"time"
	"unsafe"

	"workspace-manager/internal/applications"
	"workspace-manager/internal/domain"
)

type DesktopInstances struct{}

// Check queries the session bus daemon, never the target service: observation
// cannot auto-start it. Ownership is a point-in-time service identity, not a window
// or readiness proof. No process-name fallback and no system-bus lookup is used.
func (DesktopInstances) Check(ctx context.Context, busName string) applications.InstanceStatus {
	unknown := applications.InstanceStatus{State: "unknown", Message: "Running state is unknown. The configured session D-Bus identity could not be checked."}
	if !domain.ValidBusName(busName) || ctx.Err() != nil {
		return unknown
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cancellable := C.g_cancellable_new()
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			C.g_cancellable_cancel(cancellable)
		case <-stop:
		}
	}()
	defer func() { close(stop); <-done; C.g_object_unref(C.gpointer(cancellable)) }()
	name := C.CString(busName)
	defer C.free(unsafe.Pointer(name))
	state := C.wm_name_owner(name, cancellable)
	if ctx.Err() != nil {
		return unknown
	}
	switch state {
	case 1:
		return applications.InstanceStatus{State: "running", Message: "The configured D-Bus identity currently has an owner. Window readiness is not verified."}
	case 0:
		return applications.InstanceStatus{State: "not_running", Message: "The configured D-Bus identity currently has no owner. This does not rule out other instances."}
	default:
		return unknown
	}
}
