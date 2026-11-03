//go:build linux && cgo

package linux

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"workspace-manager/internal/domain"
	"workspace-manager/internal/lifecycle"
	"workspace-manager/internal/storage"
)

func TestDesktopScopeIdentities(t *testing.T) {
	cases := []struct {
		unit, id, instance, app string
		want                    bool
	}{
		{`app-gnome-google\x2dchrome-123.scope`, "google-chrome.desktop", "", "", true},
		{`app-gnome-google\x2dchrome\x2dbeta-123.scope`, "google-chrome.desktop", "", "", false},
		{"app-gnome-chatgpt-6606.scope", "chatgpt.desktop", "", "", true},
		{"app-dev.warp.Warp-456.scope", "dev.warp.Warp.desktop", "", "", true},
		{"app-flatpak-org.example.Editor-789.scope", "org.example.Editor.desktop", "", "", true},
		{"app-gnome-chatgpt-helper.scope", "chatgpt.desktop", "", "", false},
		{"app-gnome-chatgpt-0.scope", "chatgpt.desktop", "", "", false},
		{"app-gnome-chatgpt-123.service", "chatgpt.desktop", "", "", false},
		{"snap.brave.brave-d2d306fc-5632-4d0e-8b03-f02623e8868f.scope", "brave_brave.desktop", "brave", "brave", true},
		{"snap.code.code-123.scope", "code_code.desktop", "code", "code", true},
		{"snap.code-insiders.code-123.scope", "code_code.desktop", "code", "code", false},
		{"snap.brave.brave-123.scope", "brave_brave.desktop", "", "", false},
	}
	for _, test := range cases {
		t.Run(test.unit+test.id, func(t *testing.T) {
			if got := scopeMatches(test.unit, test.id, test.instance, test.app); got != test.want {
				t.Fatalf("match=%v, want %v", got, test.want)
			}
		})
	}
}

// Opt-in read-only integration check. It observes installed desktop identities
// on a real graphical session and never dispatches any application or command.
func TestDesktopObservationLive(t *testing.T) {
	expected := os.Getenv("WORKSPACE_MANAGER_EXPECT_DESKTOPS")
	if expected == "" {
		t.Skip("requires an explicitly requested live desktop observation")
	}
	verifyLiveLoginApproval(t)
	apps, warnings := recognizedDesktopApplications(context.Background())
	t.Log("capture warnings:", warnings)
	found := map[string]bool{}
	for _, app := range apps {
		if app.DesktopID == "io.github.workspace_manager.WorkspaceManager.desktop" {
			t.Error("manager captured itself")
		}
		found[string(app.DesktopID)] = true
		t.Log("recognized:", app.DesktopID)
	}
	for _, id := range strings.Split(os.Getenv("WORKSPACE_MANAGER_CHECK_DESKTOPS"), ",") {
		if id == "" {
			continue
		}
		entry, err := (DesktopCatalog{}).Lookup(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		status := (DesktopInstances{}).CheckDesktop(context.Background(), entry)
		t.Log("workspace duplicate check:", id, status.State)
		if status.State == "unknown" {
			t.Errorf("desktop identity remains unsupported: %s", id)
		}
	}
	for _, id := range strings.Split(expected, ",") {
		if !found[id] {
			t.Errorf("running desktop application was not captured: %s", id)
		}
		entry, err := (DesktopCatalog{}).Lookup(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if status := (DesktopInstances{}).CheckDesktop(context.Background(), entry); status.State != "running" {
			t.Errorf("%s: %+v", id, status)
		}
	}
}

// Copy only bounded launch metadata into a temporary controller. Reviewing the
// copied plan verifies the user's grant without mutating real settings/state or
// calling login/commit, launching applications, or reading environment contents.
func verifyLiveLoginApproval(t *testing.T) {
	t.Helper()
	source, err := storage.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	target := storage.Paths{ConfigDir: filepath.Join(t.TempDir(), "config"), StateDir: filepath.Join(t.TempDir(), "state"), CacheDir: filepath.Join(t.TempDir(), "cache")}
	copyRecords := func(from, to string) {
		if err := os.MkdirAll(to, 0700); err != nil {
			t.Fatal(err)
		}
		files, err := os.ReadDir(from)
		if os.IsNotExist(err) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			info, err := file.Info()
			if err != nil || info.Size() > storage.MaxDocumentBytes {
				t.Fatal("invalid metadata record")
			}
			data, err := os.ReadFile(filepath.Join(from, file.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(to, file.Name()), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	copyRecords(source.ConfigDir, target.ConfigDir)
	copyRecords(source.WorkspacesDir(), target.WorkspacesDir())
	copyRecords(source.ProjectsDir(), target.ProjectsDir())
	copyRecords(source.SessionsDir(), target.SessionsDir())
	controller, err := lifecycle.NewController(target, LifecycleHooks(), "/unused-read-only-diagnostic")
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	state := controller.Handle(context.Background(), lifecycle.Request{Action: "state"})
	if state.Code != "" || state.Settings == nil {
		t.Fatal(state.Message)
	}
	if !state.Settings.Autostart {
		t.Log("login startup is disabled")
		return
	}
	review := controller.Handle(context.Background(), lifecycle.Request{Action: "review_login", Settings: state.Settings, Revision: state.SettingsRevision})
	if review.Code != "" || review.Plan == nil {
		t.Fatal(review.Message)
	}
	data, err := os.ReadFile(filepath.Join(source.StateDir, "startup-authorizations", "00000000000000000000000000000001.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Data domain.StartupAuthorization `json:"data"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Data.Fingerprint != review.Plan.Fingerprint {
		t.Fatal("existing login approval does not match the current configuration; review Settings again")
	}
	if len(review.Plan.Tasks) == 0 {
		t.Fatal("login has no tasks")
	}
	for _, task := range review.Plan.Tasks {
		if task.Blocked != "" {
			t.Fatal("login task is blocked:", task.Name, task.Blocked)
		}
	}
	t.Log("existing login approval is valid; reviewed tasks:", len(review.Plan.Tasks))
}
