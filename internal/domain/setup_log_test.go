package domain

import (
	"strings"
	"testing"
	"time"
)

func TestSetupLogValidatesPendingAndFinalOutcomes(t *testing.T) {
	now := time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC)
	base := SetupLog{ID: ID(strings.Repeat("1", 32)), ProjectID: ID(strings.Repeat("2", 32)), Kind: "git", Label: "Initialize Git", RecordedAt: now}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"success", "failed", "no-spawn", "missing-time", "early-time", "oversize", "control", "invalid-utf8", "false-success", "kind", "identity"} {
		t.Run(scenario, func(t *testing.T) {
			log := base
			finished := now.Add(time.Second)
			log.FinishedAt = &finished
			log.Result = &SetupLogResult{Started: true, Succeeded: true, ExitCode: 0, Output: "safe\noutput\ttext", Message: "Completed."}
			valid := false
			switch scenario {
			case "success":
				valid = true
			case "failed":
				log.Result.Succeeded = false
				log.Result.ExitCode = 1
				log.Result.Code = "setup_failed"
				valid = true
			case "no-spawn":
				log.Result = &SetupLogResult{ExitCode: -1, Code: "setup_failed"}
				valid = true
			case "missing-time":
				log.FinishedAt = nil
			case "early-time":
				finished = now.Add(-time.Second)
			case "oversize":
				log.Result.Output = strings.Repeat("x", MaxSetupOutputBytes+1)
			case "control":
				log.Result.Output = "\x1b[2J"
			case "invalid-utf8":
				log.Result.Output = "\xff"
			case "false-success":
				log.Result.Started = false
			case "kind":
				log.Kind = "arbitrary-command"
			case "identity":
				log.ProjectID = "../other"
			}
			if err := log.Validate(); (err == nil) != valid {
				t.Fatal(scenario, err)
			}
		})
	}
}
