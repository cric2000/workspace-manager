package domain

import (
	"errors"
	"fmt"
)

type StartupMode string

const (
	StartupFixed   StartupMode = "fixed"
	StartupRestore StartupMode = "restore"
	StartupHybrid  StartupMode = "hybrid"
)

type Appearance string

const (
	AppearanceSystem Appearance = "system"
	AppearanceLight  Appearance = "light"
	AppearanceDark   Appearance = "dark"
)

type Settings struct {
	StartupMode         StartupMode `json:"startupMode"`
	DefaultWorkspaceID  ID          `json:"defaultWorkspaceId,omitempty"`
	RestoreSessionID    ID          `json:"restoreSessionId,omitempty"`
	Autostart           bool        `json:"autostart"`
	InitialDelaySeconds int         `json:"initialDelaySeconds"`
	Appearance          Appearance  `json:"appearance"`
}

// DefaultSettings is valid before the user creates a workspace or session.
func DefaultSettings() Settings {
	return Settings{StartupMode: StartupFixed, Appearance: AppearanceSystem}
}

func (settings Settings) Validate() error {
	switch settings.StartupMode {
	case StartupFixed, StartupRestore, StartupHybrid:
	default:
		return errors.New("startupMode must be fixed, restore or hybrid")
	}
	switch settings.Appearance {
	case AppearanceSystem, AppearanceLight, AppearanceDark:
	default:
		return errors.New("appearance must be system, light or dark")
	}
	if err := validateOptionalID(settings.DefaultWorkspaceID); err != nil {
		return fmt.Errorf("defaultWorkspaceId: %w", err)
	}
	if err := validateOptionalID(settings.RestoreSessionID); err != nil {
		return fmt.Errorf("restoreSessionId: %w", err)
	}
	if settings.InitialDelaySeconds < 0 || settings.InitialDelaySeconds > 3600 {
		return errors.New("initialDelaySeconds must be between 0 and 3600")
	}
	if settings.Autostart {
		if settings.StartupMode != StartupRestore && settings.DefaultWorkspaceID == "" {
			return errors.New("autostart mode requires a default workspace")
		}
		if settings.StartupMode != StartupFixed && settings.RestoreSessionID == "" {
			return errors.New("autostart mode requires a restore session")
		}
	}
	return nil
}
