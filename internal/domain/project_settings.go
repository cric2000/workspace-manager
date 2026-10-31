package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// CommandAction is an explicitly configured executable, never a shell string.
// It is used for editor and optional stop actions, without recursive runtime options.
type CommandAction struct {
	Executable       string   `json:"executable"`
	Arguments        []string `json:"arguments"`
	WorkingDirectory string   `json:"workingDirectory"`
}

func (action CommandAction) Validate() error {
	return validateCommand(action.Executable, action.WorkingDirectory, action.Arguments)
}

type ReadinessCheck struct {
	Kind            string `json:"kind"`
	URL             string `json:"url,omitempty"`
	Host            string `json:"host,omitempty"`
	Port            int    `json:"port,omitempty"`
	ExpectedStatus  int    `json:"expectedStatus,omitempty"`
	TimeoutSeconds  int    `json:"timeoutSeconds"`
	IntervalSeconds int    `json:"intervalSeconds"`
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

// ValidateWebURL accepts only explicit HTTP(S) URLs, with no embedded credentials.
// Validation never resolves hosts or sends requests.
func ValidateWebURL(value string) error {
	if len(value) > 4096 || validateText(value, 4096) != nil {
		return errors.New("invalid HTTP URL")
	}
	u, err := url.Parse(value)
	if err != nil || !u.IsAbs() || u.Opaque != "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || strings.HasSuffix(u.Host, ":") {
		return errors.New("use an absolute HTTP(S) URL without credentials")
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return errors.New("invalid URL port")
		}
	}
	return nil
}

func (check ReadinessCheck) Validate() error {
	if check.TimeoutSeconds < 1 || check.TimeoutSeconds > 300 || check.IntervalSeconds < 1 || check.IntervalSeconds > 30 || check.IntervalSeconds > check.TimeoutSeconds {
		return errors.New("readiness timeout must be 1–300 seconds and interval 1–30 seconds within the timeout")
	}
	switch check.Kind {
	case "http":
		if ValidateWebURL(check.URL) != nil || check.Host != "" || check.Port != 0 || check.ExpectedStatus < 200 || check.ExpectedStatus > 599 {
			return errors.New("invalid HTTP readiness check")
		}
		u, _ := url.Parse(check.URL)
		if !loopbackHost(u.Hostname()) || u.Fragment != "" {
			return errors.New("HTTP readiness must target a loopback address without a fragment")
		}
	case "tcp":
		if !loopbackHost(check.Host) || check.Port < 1 || check.Port > 65535 || check.URL != "" || check.ExpectedStatus != 0 {
			return errors.New("TCP readiness needs a loopback host and port 1–65535")
		}
	default:
		return errors.New("choose HTTP or TCP readiness")
	}
	return nil
}

type StopPolicy struct {
	Signal       string         `json:"signal"`
	GraceSeconds int            `json:"graceSeconds"`
	Action       *CommandAction `json:"action,omitempty"`
}

func (policy StopPolicy) Validate() error {
	if (policy.Signal != "SIGTERM" && policy.Signal != "SIGINT") || policy.GraceSeconds < 1 || policy.GraceSeconds > 120 {
		return errors.New("stop behavior needs SIGTERM or SIGINT and a grace period of 1–120 seconds")
	}
	if policy.Action != nil {
		return policy.Action.Validate()
	}
	return nil
}

type CommandRuntime struct {
	EnvironmentFiles []string        `json:"environmentFiles,omitempty"`
	Readiness        *ReadinessCheck `json:"readiness,omitempty"`
	Stop             *StopPolicy     `json:"stop,omitempty"`
	DelaySeconds     int             `json:"delaySeconds,omitempty"`
}

func (runtime CommandRuntime) Validate() error {
	if runtime.DelaySeconds < 0 || runtime.DelaySeconds > 3600 || len(runtime.EnvironmentFiles) > 16 {
		return errors.New("use a delay of 0–3600 seconds and at most 16 environment file references")
	}
	seen := make(map[string]bool)
	for _, path := range runtime.EnvironmentFiles {
		if len(path) > 4096 || validateText(path, 4096) != nil || path == "." || path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || path == ".." || strings.HasPrefix(path, "../") || strings.ContainsAny(path, "\r\n\t") || seen[path] {
			return errors.New("environment files must be distinct, clean project-relative paths")
		}
		seen[path] = true
	}
	if runtime.Readiness != nil {
		if err := runtime.Readiness.Validate(); err != nil {
			return err
		}
	}
	if runtime.Stop != nil {
		return runtime.Stop.Validate()
	}
	return nil
}

type ProjectPreferences struct {
	PreviewURL string           `json:"previewUrl,omitempty"`
	Editor     *CommandAction   `json:"editor,omitempty"`
	Approval   *CommandApproval `json:"approval,omitempty"`
}

func (preferences ProjectPreferences) Fingerprint() string {
	preferences.Approval = nil
	if preferences.Editor != nil {
		action := *preferences.Editor
		action.Arguments = append([]string{}, action.Arguments...)
		preferences.Editor = &action
	}
	payload, _ := json.Marshal(preferences)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func (preferences ProjectPreferences) Validate() error {
	if preferences.PreviewURL != "" {
		if err := ValidateWebURL(preferences.PreviewURL); err != nil {
			return err
		}
	}
	if preferences.Editor != nil {
		if err := preferences.Editor.Validate(); err != nil {
			return err
		}
	}
	if preferences.Approval != nil {
		if preferences.Approval.Fingerprint != preferences.Fingerprint() {
			return errors.New("approval does not match project preferences")
		}
		return validateTime(preferences.Approval.ApprovedAt)
	}
	return nil
}

func CloneAction(action *CommandAction) *CommandAction {
	if action == nil {
		return nil
	}
	copy := *action
	copy.Arguments = slices.Clone(action.Arguments)
	return &copy
}

func CloneRuntime(runtime *CommandRuntime) *CommandRuntime {
	if runtime == nil {
		return nil
	}
	copy := *runtime
	copy.EnvironmentFiles = slices.Clone(runtime.EnvironmentFiles)
	if runtime.Readiness != nil {
		check := *runtime.Readiness
		copy.Readiness = &check
	}
	if runtime.Stop != nil {
		policy := *runtime.Stop
		policy.Action = CloneAction(policy.Action)
		copy.Stop = &policy
	}
	return &copy
}

func ClonePreferences(preferences *ProjectPreferences) *ProjectPreferences {
	if preferences == nil {
		return nil
	}
	copy := *preferences
	copy.Editor = CloneAction(preferences.Editor)
	if preferences.Approval != nil {
		approval := *preferences.Approval
		copy.Approval = &approval
	}
	return &copy
}
