//go:build linux && cgo

package linux

/*
#cgo pkg-config: gtk4 gdk-pixbuf-2.0
#include <gtk/gtk.h>
#include <gdk-pixbuf/gdk-pixbuf.h>
#include <math.h>

// Called only on the GTK thread. Only local file/themed icons are supported.
static char *wm_icon_path(const char *reference) {
    GIcon *icon = g_icon_new_for_string(reference, NULL);
    if (!icon) return NULL;
    char *path = NULL;
    if (G_IS_FILE_ICON(icon)) {
        GFile *file = g_file_icon_get_file(G_FILE_ICON(icon));
        if (g_file_is_native(file)) path = g_file_get_path(file);
    } else if (G_IS_THEMED_ICON(icon)) {
        GdkDisplay *display = gdk_display_get_default();
        if (display) {
            GtkIconTheme *theme = gtk_icon_theme_get_for_display(display);
            GtkIconPaintable *paintable = gtk_icon_theme_lookup_by_gicon(theme, icon, 64, 1, GTK_TEXT_DIR_NONE, 0);
            if (paintable) {
                GFile *file = gtk_icon_paintable_get_file(paintable);
                if (file && g_file_is_native(file)) path = g_file_get_path(file);
                g_object_unref(paintable);
            }
        }
    }
    g_object_unref(icon);
    return path;
}

static void wm_icon_size(GdkPixbufLoader *loader, int width, int height, gpointer rejected) {
    if (width < 1 || height < 1 || width > 8192 || height > 8192) {
        *(gboolean *)rejected = TRUE;
        gdk_pixbuf_loader_set_size(loader, 1, 1);
        return;
    }
    double scale = MIN(1.0, MIN(64.0 / width, 64.0 / height));
    gdk_pixbuf_loader_set_size(loader, MAX(1, (int)(width * scale)), MAX(1, (int)(height * scale)));
}

// Decode memory without a file/base URI, then encode a bounded static PNG.
static guchar *wm_icon_png(const guchar *data, gsize length, gsize *output_length) {
    GdkPixbufLoader *loader = gdk_pixbuf_loader_new();
    gboolean rejected = FALSE;
    g_signal_connect(loader, "size-prepared", G_CALLBACK(wm_icon_size), &rejected);
    gboolean written = gdk_pixbuf_loader_write(loader, data, length, NULL);
    gboolean closed = gdk_pixbuf_loader_close(loader, NULL);
    guchar *output = NULL;
    if (written && closed && !rejected) {
        GdkPixbuf *pixbuf = gdk_pixbuf_loader_get_pixbuf(loader);
        if (pixbuf && gdk_pixbuf_get_width(pixbuf) <= 64 && gdk_pixbuf_get_height(pixbuf) <= 64) {
            if (!gdk_pixbuf_save_to_buffer(pixbuf, (char **)&output, output_length, "png", NULL, NULL)) {
                g_free(output);
                output = NULL;
            }
        }
    }
    g_object_unref(loader);
    return output;
}
*/
import "C"

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

var errIcon = errors.New("icon unavailable")

// DesktopIcons schedules GTK theme lookup on the desktop thread. Image I/O and
// decoding happen on the calling worker, never on GTK's UI thread.
type DesktopIcons struct{ OnMainThread func(func()) }

func (icons DesktopIcons) Render(ctx context.Context, reference string) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if icons.OnMainThread == nil || reference == "" || len(reference) > 16<<10 || strings.ContainsRune(reference, 0) {
		return nil, errIcon
	}
	ready := make(chan string, 1)
	icons.OnMainThread(func() {
		if ctx.Err() != nil {
			ready <- ""
			return
		}
		value := C.CString(reference)
		defer C.free(unsafe.Pointer(value))
		path := C.wm_icon_path(value)
		defer C.g_free(C.gpointer(path))
		ready <- C.GoString(path)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case path := <-ready:
		if path == "" {
			return nil, errIcon
		}
		return readIcon(ctx, path)
	}
}

func readIcon(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Icon symlinks are normal; nonblocking open plus fstat rejects FIFOs/devices.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errIcon
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return nil, errIcon
	}
	data, err := io.ReadAll(io.LimitReader(file, (2<<20)+1))
	if err != nil || len(data) == 0 || len(data) > 2<<20 || ctx.Err() != nil {
		return nil, errIcon
	}
	var length C.gsize
	output := C.wm_icon_png((*C.guchar)(unsafe.Pointer(&data[0])), C.gsize(len(data)), &length)
	if output == nil {
		return nil, errIcon
	}
	defer C.g_free(C.gpointer(output))
	if length == 0 || length > 64<<10 || ctx.Err() != nil {
		return nil, errIcon
	}
	return C.GoBytes(unsafe.Pointer(output), C.int(length)), nil
}
