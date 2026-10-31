//go:build linux

package linux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workspace-manager/internal/applications"
	"workspace-manager/internal/domain"
)

func TestCustomLaunchPreservesArgvAndRechecksFiles(t *testing.T) {
	for _, mode := range []string{"launch", "changed", "cancelled", "directory-changed"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			helper, capture := filepath.Join(root, "tool with spaces"), filepath.Join(root, "capture")
			script := "#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$@\" > \"" + capture + "\"\n"
			if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			custom := domain.CustomApplication{ID: domain.ID(strings.Repeat("a", 32)), Name: "Fixture", Executable: helper, WorkingDirectory: root, Arguments: []string{"two words", "", "; $HOME", "%U"}}
			launcher := CustomLauncher{}
			spec, err := launcher.ReviewCustom(context.Background(), custom)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			switch mode {
			case "changed":
				os.WriteFile(helper, []byte(script+"#changed\n"), 0o700)
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "directory-changed":
				spec.Application.WorkingDirectory = filepath.Join(root, "missing")
			}
			err = launcher.LaunchCustom(ctx, spec)
			if mode != "launch" {
				if err == nil {
					t.Fatal("invalid launch accepted")
				}
				if _, statErr := os.Stat(capture); !os.IsNotExist(statErr) {
					t.Fatal("invalid request executed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := root + "\ntwo words\n\n; $HOME\n%U\n"
			for deadline := time.Now().Add(3 * time.Second); ; {
				data, _ := os.ReadFile(capture)
				if string(data) == want {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("literal argv: %q, want %q", data, want)
				}
				time.Sleep(10 * time.Millisecond)
			}
			// Creating another file in cwd must not invalidate executable identity.
			after, err := launcher.ReviewCustom(ctx, custom)
			if err != nil || after.Fingerprint != spec.Fingerprint {
				t.Fatal("cwd activity invalidated review", err)
			}
		})
	}
}

func TestCustomLaunchUnavailableAndNoImplicitPATH(t *testing.T) {
	app := domain.CustomApplication{ID: domain.ID(strings.Repeat("b", 32)), Name: "Fixture", Executable: "true", WorkingDirectory: t.TempDir()}
	if _, err := (CustomLauncher{}).ReviewCustom(context.Background(), app); err == nil {
		t.Fatal("implicit PATH lookup allowed")
	}
	app.Executable = filepath.Join(app.WorkingDirectory, "no-execute")
	os.WriteFile(app.Executable, []byte("contents"), 0o600)
	if _, err := (CustomLauncher{}).ReviewCustom(context.Background(), app); !errors.Is(err, applications.ErrUnavailable) {
		t.Fatal(err)
	}
}
