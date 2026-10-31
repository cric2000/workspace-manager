package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

// CommandConfiguration contains literal argv, never a shell command line.
// Approval covers this configuration, not the contents of the project code.
type CommandConfiguration struct {
	Name             string          `json:"name"`
	Executable       string          `json:"executable"`
	Arguments        []string        `json:"arguments"`
	WorkingDirectory string          `json:"workingDirectory"`
	Runtime          *CommandRuntime `json:"runtime,omitempty"`
}

func (configuration CommandConfiguration) Validate() error {
	if err := validateName(configuration.Name); err != nil {
		return err
	}
	if err := validateCommand(configuration.Executable, configuration.WorkingDirectory, configuration.Arguments); err != nil {
		return err
	}
	if configuration.Runtime != nil {
		return configuration.Runtime.Validate()
	}
	return nil
}

func validateCommand(executable, directory string, arguments []string) error {
	for _, path := range []string{executable, directory} {
		if len(path) > 4096 || validatePath(path) != nil {
			return errors.New("use absolute executable and working-directory paths")
		}
	}
	if executable == "/" {
		return errors.New("executable must be a file path")
	}
	if len(arguments) > 128 {
		return errors.New("at most 128 arguments are allowed")
	}
	size := 0
	for _, argument := range arguments {
		if validateText(argument, 8192) != nil {
			return errors.New("invalid argument")
		}
		size += len(argument)
	}
	if size > 32<<10 {
		return errors.New("arguments exceed 32 KiB")
	}
	return nil
}

func (configuration CommandConfiguration) Fingerprint() string {
	// nil and empty argv have the same execution meaning.
	configuration.Arguments = append([]string{}, configuration.Arguments...)
	configuration.Runtime = CloneRuntime(configuration.Runtime)
	if configuration.Runtime != nil && configuration.Runtime.Stop != nil && configuration.Runtime.Stop.Action != nil {
		configuration.Runtime.Stop.Action.Arguments = append([]string{}, configuration.Runtime.Stop.Action.Arguments...)
	}
	payload, _ := json.Marshal(configuration)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

type CommandApproval struct {
	Fingerprint string    `json:"fingerprint"`
	ApprovedAt  time.Time `json:"approvedAt"`
}

type StartupCommand struct {
	ID            ID                   `json:"id"`
	Configuration CommandConfiguration `json:"configuration"`
	Approval      *CommandApproval     `json:"approval,omitempty"`
}

func (command StartupCommand) Validate() error {
	if err := command.ID.Validate(); err != nil {
		return err
	}
	if err := command.Configuration.Validate(); err != nil {
		return err
	}
	if command.Approval != nil {
		if command.Approval.Fingerprint != command.Configuration.Fingerprint() {
			return errors.New("approval does not match command configuration")
		}
		return validateTime(command.Approval.ApprovedAt)
	}
	return nil
}

func CloneCommands(commands []StartupCommand) []StartupCommand {
	copy := slices.Clone(commands)
	for i := range copy {
		copy[i].Configuration.Arguments = slices.Clone(copy[i].Configuration.Arguments)
		copy[i].Configuration.Runtime = CloneRuntime(copy[i].Configuration.Runtime)
		if approval := copy[i].Approval; approval != nil {
			value := *approval
			copy[i].Approval = &value
		}
	}
	return copy
}
