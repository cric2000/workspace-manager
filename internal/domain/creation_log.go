package domain

import (
	"errors"
	"slices"
	"time"
	"unicode"
	"unicode/utf8"
)

// CreationLog is diagnostic history, independent of registration and approvals.
// Source files, command arguments and review tokens are deliberately excluded.
type CreationLog struct {
	ID         ID                 `json:"id"`
	ProjectID  ID                 `json:"projectId"`
	Name       string             `json:"name"`
	Path       string             `json:"path"`
	TemplateID string             `json:"templateId"`
	Kind       string             `json:"kind"`
	RecordedAt time.Time          `json:"recordedAt"`
	FinishedAt *time.Time         `json:"finishedAt,omitempty"`
	Result     *CreationLogResult `json:"result,omitempty"`
}

type CreationLogResult struct {
	Succeeded      bool     `json:"succeeded"`
	Created        bool     `json:"created"`
	Registered     bool     `json:"registered"`
	ProcessStarted bool     `json:"processStarted"`
	ExitCode       int      `json:"exitCode"`
	FileCount      int      `json:"fileCount"`
	RecoveryPath   string   `json:"recoveryPath,omitempty"`
	Activity       []string `json:"activity"`
	Output         string   `json:"output"`
	Truncated      bool     `json:"truncated"`
	Code           string   `json:"code"`
	Message        string   `json:"message"`
}

func (log CreationLog) Validate() error {
	if err := log.ID.Validate(); err != nil {
		return err
	}
	if err := log.ProjectID.Validate(); err != nil {
		return err
	}
	if err := validateName(log.Name); err != nil {
		return err
	}
	if err := validatePath(log.Path); err != nil {
		return err
	}
	if len(log.Path) > 4096 {
		return errors.New("creation path exceeds limit")
	}
	if !slices.Contains([]string{"go", "node-typescript", "python", "fastapi", "nextjs"}, log.TemplateID) {
		return errors.New("invalid creation template")
	}
	if log.Kind != "preparation" && log.Kind != "publication" {
		return errors.New("invalid creation stage")
	}
	if log.Kind == "preparation" && log.TemplateID != "nextjs" {
		return errors.New("only Next.js uses scaffolder preparation")
	}
	if err := validateTime(log.RecordedAt); err != nil {
		return err
	}
	if log.Result == nil {
		if log.FinishedAt != nil {
			return errors.New("unfinished attempt has a finish time")
		}
		return nil
	}
	if log.FinishedAt == nil {
		return errors.New("finished attempt requires a finish time")
	}
	if err := validateTimes(log.RecordedAt, *log.FinishedAt); err != nil {
		return err
	}
	r := log.Result
	if r.FileCount < 0 || r.FileCount > 64 || r.ExitCode < -1 || r.ExitCode > 255 || r.Registered && !r.Created {
		return errors.New("invalid creation outcome")
	}
	if log.Kind == "preparation" && (r.Created || r.Registered) {
		return errors.New("preparation cannot publish or register")
	}
	if log.Kind == "publication" && r.ProcessStarted {
		return errors.New("publication never executes a process")
	}
	if r.Succeeded && (r.Code != "" || r.FileCount == 0 || log.Kind == "publication" && (!r.Created || !r.Registered)) {
		return errors.New("inconsistent successful outcome")
	}
	if r.RecoveryPath != "" {
		if err := validatePath(r.RecoveryPath); err != nil {
			return err
		}
		if len(r.RecoveryPath) > 4096 {
			return errors.New("recovery path exceeds limit")
		}
	}
	if len(r.Output) > MaxSetupOutputBytes || !utf8.ValidString(r.Output) {
		return errors.New("invalid creation output")
	}
	for _, c := range r.Output {
		if unicode.IsControl(c) && c != '\n' && c != '\t' {
			return errors.New("terminal controls in creation output")
		}
	}
	if len(r.Activity) > 8 {
		return errors.New("too many creation activity entries")
	}
	for _, line := range r.Activity {
		if err := validateText(line, 512); err != nil {
			return err
		}
	}
	if err := validateText(r.Code, 64); err != nil {
		return err
	}
	return validateText(r.Message, 1024)
}
