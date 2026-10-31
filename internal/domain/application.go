package domain

import (
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrInvalidDesktopID = errors.New("invalid desktop application ID")

type DesktopID string

// Desktop IDs accept legacy names too, but never paths, URIs or command lines.
func (id DesktopID) Validate() error {
	value := string(id)
	if !utf8.ValidString(value) || len(value) > 4096 || len(value) <= len(".desktop") || !strings.HasSuffix(value, ".desktop") || strings.ContainsRune(value, '/') {
		return ErrInvalidDesktopID
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return ErrInvalidDesktopID
		}
	}
	return nil
}

type DuplicatePolicy string

const (
	SkipIfRunning            DuplicatePolicy = "skip_if_running"
	AlwaysLaunch             DuplicatePolicy = "always_launch"
	MaxWorkspaceApplications                 = 64
)

// WorkspaceApplication stores configuration, not command approval or runtime
// identity. Slice position defines launch order. Resolve desktop metadata afresh
// before a reviewed launch; never infer an existing instance from a process name.
type WorkspaceApplication struct {
	DesktopID       DesktopID          `json:"desktopId"`
	DuplicatePolicy DuplicatePolicy    `json:"duplicatePolicy"`
	Custom          *CustomApplication `json:"custom,omitempty"`
}

func (application WorkspaceApplication) Validate() error {
	if application.Custom != nil {
		if application.DesktopID != "" {
			return errors.New("choose a desktop ID or a custom executable")
		}
		if err := application.Custom.Validate(); err != nil {
			return err
		}
	} else if err := application.DesktopID.Validate(); err != nil {
		return err
	}
	if application.DuplicatePolicy != SkipIfRunning && application.DuplicatePolicy != AlwaysLaunch {
		return errors.New("invalid duplicate policy")
	}
	return nil
}

// CustomApplication is workspace-scoped configuration, never launch approval.
// Arguments are literal argv entries. No PATH, shell or tilde expansion occurs.
type CustomApplication struct {
	ID               ID       `json:"id"`
	Name             string   `json:"name"`
	Executable       string   `json:"executable"`
	Arguments        []string `json:"arguments"`
	WorkingDirectory string   `json:"workingDirectory"`
	InstanceBusName  string   `json:"instanceBusName"`
}

func (app CustomApplication) Validate() error {
	if err := app.ID.Validate(); err != nil {
		return err
	}
	if err := validateName(app.Name); err != nil {
		return err
	}
	if err := validateCommand(app.Executable, app.WorkingDirectory, app.Arguments); err != nil {
		return err
	}
	if app.InstanceBusName != "" && !ValidBusName(app.InstanceBusName) {
		return errors.New("invalid well-known D-Bus identity")
	}
	return nil
}

// Accept well-known names, never unique connection names or the bus itself.
func ValidBusName(name string) bool {
	if len(name) > 255 || name == "org.freedesktop.DBus" {
		return false
	}
	parts := strings.Split(name, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part[0] >= '0' && part[0] <= '9' {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
				return false
			}
		}
	}
	return true
}

func CloneApplications(apps []WorkspaceApplication) []WorkspaceApplication {
	copy := slices.Clone(apps)
	for i := range copy {
		if copy[i].Custom != nil {
			custom := *copy[i].Custom
			custom.Arguments = slices.Clone(custom.Arguments)
			copy[i].Custom = &custom
		}
	}
	return copy
}
