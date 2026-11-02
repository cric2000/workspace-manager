package domain

import (
	"strings"
	"testing"
	"time"
)

func TestCreationLogValidatesLifecycleAndSeparatesPreparationFromPublication(t *testing.T) {
	now := time.Now().UTC()
	base := CreationLog{ID: "11111111111111111111111111111111", ProjectID: "22222222222222222222222222222222", Name: "example", Path: "/tmp/example", TemplateID: "nextjs", Kind: "preparation", RecordedAt: now}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"preparation", "publication", "failure", "registered-without-files", "prep-published", "process-in-publication", "unknown-template", "oversize", "terminal-control", "invalid-path", "missing-finish"} {
		t.Run(scenario, func(t *testing.T) {
			log := base
			log.FinishedAt = &now
			log.Result = &CreationLogResult{Succeeded: true, ProcessStarted: true, FileCount: 2}
			valid := false
			switch scenario {
			case "preparation":
				valid = true
			case "publication":
				log.Kind = "publication"
				log.Result.ProcessStarted = false
				log.Result.Created = true
				log.Result.Registered = true
				log.Result.ExitCode = -1
				valid = true
			case "failure":
				log.Result = &CreationLogResult{Code: "cancelled", ExitCode: -1}
				valid = true
			case "registered-without-files":
				log.Result.Registered = true
			case "prep-published":
				log.Result.Created = true
			case "process-in-publication":
				log.Kind = "publication"
				log.Result.Created = true
				log.Result.Registered = true
			case "unknown-template":
				log.TemplateID = "untrusted"
			case "oversize":
				log.Result.Output = strings.Repeat("x", MaxSetupOutputBytes+1)
			case "terminal-control":
				log.Result.Output = "\x1b[2J"
			case "invalid-path":
				log.Path = "../project"
			case "missing-finish":
				log.FinishedAt = nil
			}
			if err := log.Validate(); (err == nil) != valid {
				t.Fatal(scenario, err)
			}
		})
	}
}
