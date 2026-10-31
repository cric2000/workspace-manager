// Package applications defines application metadata independently of the desktop
// toolkit. Discovery never grants approval to execute an application or command.
package applications

import (
	"errors"
	"workspace-manager/internal/domain"
)

var (
	ErrInvalidDesktopID = domain.ErrInvalidDesktopID
	ErrUnavailable      = errors.New("desktop application is not available")
)

// DesktopEntry is a point-in-time registration, not a running process or a
// readiness result. Persist ID in profiles; resolve current metadata before use.
type DesktopEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	GenericName string `json:"genericName"`
	Description string `json:"description"`
	DesktopFile string `json:"desktopFile"`
	// Icon is a serialized GIcon reference, not image bytes or a web URL.
	Icon string `json:"icon"`
	// Commandline and Executable are display metadata. Never split Commandline
	// with a shell, or spawn Executable instead of the platform launch API.
	Commandline      string `json:"commandline"`
	Executable       string `json:"executable"`
	WorkingDirectory string `json:"workingDirectory"`
	Terminal         bool   `json:"terminal"`
	DBusActivatable  bool   `json:"dbusActivatable"`
	Visible          bool   `json:"visible"`
}

// ValidateDesktopID accepts legacy IDs as well as reverse-DNS names. A desktop
// ID is not a path, URI, command or user-chosen file outside the XDG catalogue.
func ValidateDesktopID(id string) error {
	return domain.DesktopID(id).Validate()
}
