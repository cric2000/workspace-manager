package applications

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureCatalog struct {
	entries []DesktopEntry
	err     error
}

func (catalog fixtureCatalog) Discover(context.Context) ([]DesktopEntry, error) {
	return catalog.entries, catalog.err
}
func (catalog fixtureCatalog) Lookup(_ context.Context, id string) (DesktopEntry, error) {
	for _, entry := range catalog.entries {
		if entry.ID == id {
			return entry, catalog.err
		}
	}
	return DesktopEntry{}, ErrUnavailable
}

type fixtureRenderer func(context.Context, string) ([]byte, error)

func (render fixtureRenderer) Render(ctx context.Context, icon string) ([]byte, error) {
	return render(ctx, icon)
}
func pngFixture(t *testing.T, width, height int) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
func catalogEntry() DesktopEntry {
	return DesktopEntry{ID: "org.example.Editor.desktop", Name: "Editor", Icon: "editor-icon", Visible: true}
}

func TestCatalogServiceSafeLoadingAndExposure(t *testing.T) {
	entry := catalogEntry()
	hidden, invalid := entry, entry
	hidden.ID, hidden.Visible, invalid.ID = "org.example.NonMenu.desktop", false, "/private/file.desktop"
	service := NewService(fixtureCatalog{entries: []DesktopEntry{entry, hidden, invalid}}, nil)
	result := service.List(context.Background())
	if result.Code != "" || len(result.Entries) != 1 || result.Entries[0].ID != entry.ID {
		t.Fatal(result)
	}
	broken := NewService(fixtureCatalog{err: errors.New("private path and secret")}, nil).List(context.Background())
	if broken.Code != "discovery_failed" || len(broken.Entries) != 0 || strings.Contains(broken.Message, "secret") {
		t.Fatal(broken)
	}
	empty := NewService(fixtureCatalog{}, nil).List(context.Background())
	if empty.Code != "" || empty.Entries == nil {
		t.Fatal(empty)
	}
}

func TestCatalogIconUsesRegistrationAndReturnsOnlyBoundedPNG(t *testing.T) {
	entry := catalogEntry()
	data := pngFixture(t, 64, 32)
	var calls int
	service := NewService(fixtureCatalog{entries: []DesktopEntry{entry}}, fixtureRenderer(func(_ context.Context, reference string) ([]byte, error) {
		calls++
		if reference != entry.Icon {
			t.Fatal("renderer did not use registered icon")
		}
		return data, nil
	}))
	result := service.GetIcon(context.Background(), entry.ID)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(result.DataURL, "data:image/png;base64,"))
	if err != nil || !bytes.Equal(decoded, data) || calls != 1 {
		t.Fatal(result, err, calls)
	}
	for _, id := range []string{"/private/file.png", "https://example.org/icon.desktop", "org.example.Missing.desktop"} {
		if result := service.GetIcon(context.Background(), id); result.DataURL != "" {
			t.Fatal(result)
		}
	}
	if calls != 1 {
		t.Fatal("invalid/missing ID reached the renderer")
	}
	for _, invalid := range [][]byte{[]byte("<svg>not PNG</svg>"), pngFixture(t, 65, 64), make([]byte, (64<<10)+1)} {
		data = invalid
		if result := service.GetIcon(context.Background(), entry.ID); result.DataURL != "" {
			t.Fatal("invalid icon passed the boundary")
		}
	}
	entry.Visible = false
	service = NewService(fixtureCatalog{entries: []DesktopEntry{entry}}, fixtureRenderer(func(context.Context, string) ([]byte, error) { t.Fatal("non-menu icon rendered"); return nil, nil }))
	if service.GetIcon(context.Background(), entry.ID).DataURL != "" {
		t.Fatal("non-menu entry exposed")
	}
}

func TestCatalogIconConcurrencyAndCancellation(t *testing.T) {
	entry := catalogEntry()
	var active, maximum atomic.Int32
	entered, release := make(chan struct{}, 4), make(chan struct{})
	service := NewService(fixtureCatalog{entries: []DesktopEntry{entry}}, fixtureRenderer(func(context.Context, string) ([]byte, error) {
		count := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); count > old && !maximum.CompareAndSwap(old, count); old = maximum.Load() {
		}
		entered <- struct{}{}
		<-release
		return nil, errors.New("missing icon")
	}))
	var wait sync.WaitGroup
	for range 4 {
		wait.Add(1)
		go func() { defer wait.Done(); service.GetIcon(context.Background(), entry.ID) }()
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("icon worker did not start")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan IconResult, 1)
	go func() { done <- service.GetIcon(ctx, entry.ID) }()
	cancel()
	select {
	case result := <-done:
		if result.DataURL != "" {
			t.Fatal(result)
		}
	case <-time.After(time.Second):
		t.Fatal("queued icon did not cancel")
	}
	close(release)
	wait.Wait()
	if maximum.Load() != 4 || active.Load() != 0 {
		t.Fatal("unexpected icon concurrency", maximum.Load())
	}
}
