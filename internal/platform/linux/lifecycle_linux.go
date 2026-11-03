package linux

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os/exec"
	"time"

	"github.com/godbus/dbus/v5"
	"workspace-manager/internal/applications"
	"workspace-manager/internal/domain"
	"workspace-manager/internal/lifecycle"
)

func actionApplication(action domain.CommandAction) domain.CustomApplication {
	return domain.CustomApplication{ID: "00000000000000000000000000000002", Name: "Reviewed project action", Executable: action.Executable, Arguments: action.Arguments, WorkingDirectory: action.WorkingDirectory}
}
func ReviewAction(ctx context.Context, action domain.CommandAction) (string, error) {
	spec, err := (CustomLauncher{}).ReviewCustom(ctx, actionApplication(action))
	return spec.Fingerprint, err
}
func LifecycleHooks() lifecycle.Hooks {
	catalog := DesktopCatalog{}
	custom := CustomLauncher{}
	instances := DesktopInstances{}
	reviewApp := func(ctx context.Context, app domain.WorkspaceApplication) (string, error) {
		if err := app.Validate(); err != nil {
			return "", err
		}
		var identity string
		if app.Custom != nil {
			spec, err := custom.ReviewCustom(ctx, *app.Custom)
			if err != nil {
				return "", err
			}
			identity = spec.Fingerprint
		} else {
			spec, err := catalog.Review(ctx, string(app.DesktopID))
			if err != nil {
				return "", err
			}
			identity = spec.Fingerprint
		}
		data, _ := json.Marshal(struct {
			App      domain.WorkspaceApplication
			Identity string
		}{app, identity})
		hash := sha256.Sum256(data)
		return hex.EncodeToString(hash[:]), nil
	}
	return lifecycle.Hooks{
		ReviewAction: ReviewAction,
		LaunchAction: func(ctx context.Context, action domain.CommandAction) error {
			spec, err := custom.ReviewCustom(ctx, actionApplication(action))
			if err != nil {
				return err
			}
			return custom.LaunchCustom(ctx, spec)
		},
		ReviewApplication: reviewApp,
		DescribeApplication: func(ctx context.Context, app domain.WorkspaceApplication) *applications.DesktopEntry {
			if app.Custom != nil {
				return nil
			}
			entry, err := catalog.Lookup(ctx, string(app.DesktopID))
			if err != nil {
				return nil
			}
			return &entry
		},
		LaunchApplication: func(ctx context.Context, app domain.WorkspaceApplication) error {
			if app.DuplicatePolicy == domain.SkipIfRunning {
				var status applications.InstanceStatus
				if app.Custom != nil {
					status = instances.Check(ctx, app.Custom.InstanceBusName)
				} else {
					entry, err := catalog.Lookup(ctx, string(app.DesktopID))
					if err != nil {
						return err
					}
					status = instances.CheckDesktop(ctx, entry)
				}
				if status.State == "running" {
					return nil
				}
				if status.State != "not_running" {
					return errors.New("application instance identity cannot be confirmed")
				}
			}
			if app.Custom != nil {
				spec, err := custom.ReviewCustom(ctx, *app.Custom)
				if err != nil {
					return err
				}
				return custom.LaunchCustom(ctx, spec)
			}
			spec, err := catalog.Review(ctx, string(app.DesktopID))
			if err != nil {
				return err
			}
			return catalog.Launch(ctx, spec)
		},
		RecognizedApplications: recognizedDesktopApplications,
		OpenURL: func(ctx context.Context, value string) error {
			if domain.ValidateWebURL(value) != nil {
				return errors.New("invalid preview URL")
			}
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return exec.CommandContext(ctx, "/usr/bin/gio", "open", value).Run()
		},
		Shutdown: func(ctx context.Context) error {
			// GNOME's normal session shutdown coordinates applications and their save
			// dialogs. There is no force command, inhibitor bypass or fallback shell.
			bus, err := dbus.ConnectSessionBus()
			if err != nil {
				return err
			}
			defer bus.Close()
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			return bus.Object("org.gnome.SessionManager", dbus.ObjectPath("/org/gnome/SessionManager")).CallWithContext(ctx, "org.gnome.SessionManager.Shutdown", 0).Err
		},
	}
}

// Compile-time assertions retain the existing reviewed desktop launch contract.
var _ applications.CustomLauncher = CustomLauncher{}
