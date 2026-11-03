//go:build linux && cgo && mcp

package main

/*
#cgo pkg-config: gtk4 webkitgtk-6.0
#include <gtk/gtk.h>
#include <webkit/webkit.h>
#include <stdint.h>

extern void screenshotReady(uintptr_t handle, char *base64);
static WebKitWebView *find_view(GtkWidget *widget) {
 if (WEBKIT_IS_WEB_VIEW(widget)) return WEBKIT_WEB_VIEW(widget);
 for (GtkWidget *child = gtk_widget_get_first_child(widget); child; child = gtk_widget_get_next_sibling(child)) {
  WebKitWebView *view = find_view(child); if (view) return view;
 }
 return NULL;
}
static void snapshot_ready(GObject *source, GAsyncResult *result, gpointer data) {
 GError *error = NULL;
 GdkTexture *texture = webkit_web_view_get_snapshot_finish(WEBKIT_WEB_VIEW(source), result, &error);
 char *encoded = NULL;
 if (texture) {
  GBytes *bytes = gdk_texture_save_to_png_bytes(texture);
  gsize length = 0; const guchar *png = g_bytes_get_data(bytes, &length);
  if (length <= 4 * 1024 * 1024) encoded = g_base64_encode(png, length);
  g_bytes_unref(bytes); g_object_unref(texture);
 }
 if (error) g_error_free(error);
 screenshotReady((uintptr_t)data, encoded);
 g_free(encoded);
}
static void capture_view(void *window, uintptr_t handle) {
 WebKitWebView *view = window ? find_view(GTK_WIDGET(window)) : NULL;
 if (!view) { screenshotReady(handle, NULL); return; }
 webkit_web_view_get_snapshot(view, WEBKIT_SNAPSHOT_REGION_FULL_DOCUMENT, WEBKIT_SNAPSHOT_OPTIONS_NONE,
                             NULL, snapshot_ready, (gpointer)handle);
}
*/
import "C"

import (
	"context"
	"errors"
	"github.com/wailsapp/wails/v3/pkg/application"
	"runtime/cgo"
)

// ScreenshotService exists only in authenticated MCP smoke builds. It captures
// the app WebView, accepts no filesystem path and is absent from production.
type ScreenshotService struct {
	window func() *application.WebviewWindow
}

func (s *ScreenshotService) Capture(ctx context.Context) (string, error) {
	result := make(chan string, 1)
	handle := cgo.NewHandle(result)
	application.InvokeAsync(func() {
		window := s.window()
		if window == nil {
			screenshotReady(C.uintptr_t(handle), nil)
			return
		}
		C.capture_view(window.NativeWindow(), C.uintptr_t(handle))
	})
	select {
	case value := <-result:
		if value == "" {
			return "", errors.New("native WebView capture failed")
		}
		return value, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

//export screenshotReady
func screenshotReady(handle C.uintptr_t, value *C.char) {
	token := cgo.Handle(handle)
	result := token.Value().(chan string)
	token.Delete()
	result <- C.GoString(value)
}
func smokeServices(window func() *application.WebviewWindow) []application.Service {
	return []application.Service{application.NewService(&ScreenshotService{window: window})}
}
