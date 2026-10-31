//go:build linux && cgo

package linux

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Decoder helpers may create their own caches. Keep them below a process-owned
// temporary root, set before GLib caches the environment. Catalogue fixture
// subprocesses already receive their own isolated XDG cache paths.
func TestMain(tests *testing.M) {
	if os.Getenv("WORKSPACE_DESKTOP_FIXTURE_CHILD") == "1" {
		os.Exit(tests.Run())
	}
	cache, err := os.MkdirTemp("", "workspace-desktop-test-cache-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CACHE_HOME", cache); err != nil {
		panic(err)
	}
	code := tests.Run()
	_ = os.RemoveAll(cache)
	os.Exit(code)
}

func TestLocalIconRenderingProducesSmallPNG(t *testing.T) {
	root := t.TempDir()
	var bitmap bytes.Buffer
	if err := png.Encode(&bitmap, image.NewNRGBA(image.Rect(0, 0, 300, 150))); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name          string
		data          []byte
		width, height int
	}{
		{"wide.png", bitmap.Bytes(), 64, 32},
		{"vector.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="128" height="128"><rect width="128" height="128" fill="#285c42"/></svg>`), 64, 64},
	} {
		path := filepath.Join(root, test.name)
		if err := os.WriteFile(path, test.data, 0o600); err != nil {
			t.Fatal(err)
		}
		icons := DesktopIcons{OnMainThread: func(fn func()) { fn() }} // File icons need no GTK display.
		data, err := icons.Render(context.Background(), path)
		if err != nil {
			t.Fatal(test.name, err)
		}
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width != test.width || config.Height != test.height {
			t.Fatal(config, err)
		}
	}
}

func TestIconReadRejectsUnsupportedInputs(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string][]byte{
		"not-an-image": []byte("private plain text"), "oversized": make([]byte, (2<<20)+1),
		"giant.svg":  []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="1000000" height="1000000"/>`),
		"broken.svg": []byte(`<svg broken`),
	} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readIcon(context.Background(), path); err == nil {
			t.Fatal("accepted", name)
		}
	}
	fifo := filepath.Join(root, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readIcon(context.Background(), fifo); err == nil {
		t.Fatal("accepted FIFO")
	}
	if _, err := readIcon(context.Background(), root); err == nil {
		t.Fatal("accepted directory")
	}
	icons := DesktopIcons{OnMainThread: func(fn func()) { fn() }}
	for _, reference := range []string{"https://example.org/image.png", "sftp://example.org/image.png", "broken\x00reference"} {
		if _, err := icons.Render(context.Background(), reference); err == nil {
			t.Fatal("accepted non-local reference")
		}
	}
}

func TestIconGTKDispatchCancelsWithoutWaitingForMainLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan func(), 1)
	icons := DesktopIcons{OnMainThread: func(fn func()) { queued <- fn }}
	done := make(chan error, 1)
	go func() { _, err := icons.Render(ctx, "editor-icon"); done <- err }()
	var callback func()
	select {
	case callback = <-queued:
	case <-time.After(time.Second):
		t.Fatal("GTK work was not queued")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("icon did not cancel")
	}
	callback() // A late main-loop callback does not block or touch GTK after cancel.
}
