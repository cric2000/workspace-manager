// Package app contains the small service facade exposed to the desktop UI.
package app

import (
	"os"
	"runtime"
)

const Version = "0.1.0-dev"

// Info is an allowlist of non-sensitive diagnostics, not an environment dump.
type Info struct {
	Version      string `json:"version"`
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	GoVersion    string `json:"goVersion"`
	SessionType  string `json:"sessionType"`
}

// InfoService is read-only and does not start processes or write configuration.
type InfoService struct{}

// GetInfo reports the runtime actually serving the desktop bridge.
func (*InfoService) GetInfo() Info {
	session := os.Getenv("XDG_SESSION_TYPE")
	switch session {
	case "wayland", "x11":
	default:
		session = "unknown"
	}
	return Info{
		Version: Version, OS: runtime.GOOS, Architecture: runtime.GOARCH,
		GoVersion: runtime.Version(), SessionType: session,
	}
}
