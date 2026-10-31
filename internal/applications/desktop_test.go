package applications

import (
	"errors"
	"strings"
	"testing"
)

func TestDesktopIDsAreIdentifiersRatherThanPathsOrCommands(t *testing.T) {
	for _, id := range []string{"org.gnome.Terminal.desktop", "google-chrome.desktop", "vendor-editor.desktop", "Code Editor.desktop"} {
		if err := ValidateDesktopID(id); err != nil {
			t.Fatalf("rejected installed-style ID %q: %v", id, err)
		}
	}
	for _, id := range []string{"", ".desktop", "../org.example.App.desktop", "/tmp/org.example.App.desktop", "https://example.org/app.desktop", "app.desktop\x00", "app\n.desktop", "app.desktop --argument", "app.Desktop", "\xff.desktop", strings.Repeat("a", 4097) + ".desktop"} {
		if err := ValidateDesktopID(id); !errors.Is(err, ErrInvalidDesktopID) {
			t.Fatalf("accepted invalid ID %q: %v", id, err)
		}
	}
}
