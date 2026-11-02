package domain

import (
	"errors"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxSetupOutputBytes = 64 << 10

// SetupLog contains diagnostics, never approval tokens, environment maps or
// executable instructions. A missing result means no final outcome was saved.
type SetupLog struct {
	ID         ID              `json:"id"`
	ProjectID  ID              `json:"projectId"`
	Kind       string          `json:"kind"`
	Label      string          `json:"label"`
	RecordedAt time.Time       `json:"recordedAt"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
	Result     *SetupLogResult `json:"result,omitempty"`
}

type SetupLogResult struct {
	Started   bool   `json:"started"`
	Succeeded bool   `json:"succeeded"`
	ExitCode  int    `json:"exitCode"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

func (log SetupLog) Validate() error {
	if err := log.ID.Validate(); err != nil {
		return err
	}
	if err := log.ProjectID.Validate(); err != nil {
		return err
	}
	if log.Kind != "git" && log.Kind != "environment" && log.Kind != "dependencies" {
		return errors.New("invalid setup kind")
	}
	if err := validateName(log.Label); err != nil {
		return err
	}
	if err := validateTime(log.RecordedAt); err != nil {
		return err
	}
	if log.Result == nil {
		if log.FinishedAt != nil {
			return errors.New("unfinished setup must not have a finish time")
		}
		return nil
	}
	if log.FinishedAt == nil {
		return errors.New("finished setup requires a finish time")
	}
	if err := validateTimes(log.RecordedAt, *log.FinishedAt); err != nil {
		return err
	}
	result := log.Result
	if result.ExitCode < -1 || result.ExitCode > 255 || result.Succeeded && (!result.Started || result.ExitCode != 0 || result.Code != "") {
		return errors.New("invalid setup outcome")
	}
	if len(result.Output) > MaxSetupOutputBytes || !utf8.ValidString(result.Output) {
		return errors.New("invalid setup output size or encoding")
	}
	for _, r := range result.Output {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return errors.New("setup output contains terminal controls")
		}
	}
	if err := validateText(result.Code, 64); err != nil {
		return err
	}
	return validateText(result.Message, 1024)
}
