package applications

import (
	"bytes"
	"context"
	"encoding/base64"
	"image/png"
)

// Catalog is implemented by the Linux GIO adapter. UI clients supply IDs only.
type Catalog interface {
	Discover(context.Context) ([]DesktopEntry, error)
	Lookup(context.Context, string) (DesktopEntry, error)
}

type IconRenderer interface {
	Render(context.Context, string) ([]byte, error)
}

type CatalogResult struct {
	Entries []DesktopEntry `json:"entries"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
}

type IconResult struct {
	DataURL string `json:"dataUrl"`
}

// Service exposes read-only catalogue operations. Missing/broken icons use the
// UI's generic fallback and never make the application catalogue unavailable.
type Service struct {
	catalog   Catalog
	icons     IconRenderer
	iconSlots chan struct{}
}

func NewService(catalog Catalog, icons IconRenderer) *Service {
	return &Service{catalog: catalog, icons: icons, iconSlots: make(chan struct{}, 4)}
}

func (service *Service) List(ctx context.Context) CatalogResult {
	entries, err := service.catalog.Discover(ctx)
	if err != nil || ctx.Err() != nil {
		return CatalogResult{Entries: []DesktopEntry{}, Code: "discovery_failed", Message: "Could not read installed applications. Refresh the list to try again."}
	}
	visible := make([]DesktopEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Visible && ValidateDesktopID(entry.ID) == nil {
			visible = append(visible, entry)
		}
	}
	return CatalogResult{Entries: visible}
}

// GetIcon re-resolves a registration; it accepts neither paths nor GIcon strings
// from the WebView. At most four image reads/conversions run concurrently.
func (service *Service) GetIcon(ctx context.Context, id string) IconResult {
	if ctx.Err() != nil || ValidateDesktopID(id) != nil || service.icons == nil {
		return IconResult{}
	}
	select {
	case service.iconSlots <- struct{}{}:
		defer func() { <-service.iconSlots }()
	case <-ctx.Done():
		return IconResult{}
	}
	if ctx.Err() != nil {
		return IconResult{}
	}
	entry, err := service.catalog.Lookup(ctx, id)
	if err != nil || !entry.Visible || entry.Icon == "" {
		return IconResult{}
	}
	data, err := service.icons.Render(ctx, entry.Icon)
	if err != nil || ctx.Err() != nil || len(data) > 64<<10 {
		return IconResult{}
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 64 || config.Height > 64 {
		return IconResult{}
	}
	return IconResult{DataURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)}
}
