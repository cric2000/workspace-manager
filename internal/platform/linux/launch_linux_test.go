//go:build linux && cgo

package linux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"workspace-manager/internal/applications"
)

func TestDesktopLaunchThroughGIO(t *testing.T) {
	for _, mode := range []string{"requested", "changed", "cancelled", "hidden", "failure"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newDesktopFixture(t)
			fixture.launchMode = mode
			directory := filepath.Join(fixture.root, "working directory")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			capture := filepath.Join(fixture.root, "captured-arguments")
			helper := filepath.Join(directory, "capture arguments")
			// This test-owned helper exits immediately. No installed app is run.
			script := "#!/bin/sh\noutput=$1\nshift\n{ printf '%s\\n' \"$PWD\"; printf '%s\\n' \"$@\"; } > \"$output\"\n"
			if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			id := "org.example.LaunchFixture.desktop"
			contents := "[Desktop Entry]\nType=Application\nName=Fixture Launcher\nExec=\"" + helper + "\" \"" + capture + "\" \"two words\" \"; literal\" %% %c %k %f %u\nPath=" + directory + "\n"
			if mode == "failure" {
				contents += "Path=" + filepath.Join(fixture.root, "missing-directory") + "\n"
			}
			desktop := desktopFile(t, fixture.user, id, contents)
			if mode == "hidden" {
				desktopFile(t, fixture.first, id, contents)
				desktopFile(t, fixture.user, id, "[Desktop Entry]\nHidden=true\n")
			}
			got := fixtureCatalogue(t, fixture, "GNOME", "C.UTF-8", id)
			expected := mode
			if mode == "hidden" {
				expected = "unavailable"
			}
			if mode == "failure" {
				expected = "failed"
			}
			if got.LaunchError != expected {
				t.Fatalf("launch: %s, expected %s", got.LaunchError, expected)
			}
			if mode != "requested" {
				if _, err := os.Stat(capture); !os.IsNotExist(err) {
					t.Fatalf("unexpected launch: %v", err)
				}
				return
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				data, err := os.ReadFile(capture)
				want := strings.Join([]string{directory, "two words", "; literal", "%", "Fixture Launcher", desktop, ""}, "\n")
				if err == nil && string(data) == want {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("launch arguments/cwd: %q (%v), want %q", data, err, want)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if got.Lookups[id].Entry.WorkingDirectory != directory {
				t.Fatal("review omitted working directory")
			}
		})
	}
}

func TestLaunchFingerprintRejectsNonregularAndOversizedFiles(t *testing.T) {
	fixture := newDesktopFixture(t)
	path := desktopFile(t, fixture.user, "example.desktop", applicationFile("Example", ""))
	entry := applications.DesktopEntry{ID: "example.desktop", DesktopFile: path}
	first, err := launchFingerprint(entry)
	if err != nil {
		t.Fatal(err)
	}
	entry.Terminal = true
	second, err := launchFingerprint(entry)
	if err != nil || first == second {
		t.Fatal("effective metadata missing from fingerprint")
	}
	if err := os.WriteFile(path, make([]byte, (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := launchFingerprint(entry); err == nil {
		t.Fatal("oversized file accepted")
	}
	entry.DesktopFile = filepath.Dir(path)
	if _, err := launchFingerprint(entry); err == nil {
		t.Fatal("directory accepted")
	}
	entry.DesktopFile = filepath.Join(fixture.root, "fifo")
	if err := unix.Mkfifo(entry.DesktopFile, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := launchFingerprint(entry); err == nil {
		t.Fatal("FIFO accepted")
	}
}
