package domain

import (
	"encoding/hex"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// BootstrapRecovery is an operational checkpoint, separate from diagnostic
// history. Its configuration never grants approval to execute anything.
type BootstrapRecovery struct {
	ID         ID                  `json:"id"`
	ProjectID  ID                  `json:"projectId"`
	Path       string              `json:"path"`
	Kind       string              `json:"kind"`
	RecordedAt time.Time           `json:"recordedAt"`
	Project    *Project            `json:"project,omitempty"`
	Files      []RecoveryFile      `json:"files,omitempty"`
	Assessment *RecoveryAssessment `json:"assessment,omitempty"`
}
type RecoveryFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type RecoveryAssessment struct {
	CheckedAt    time.Time `json:"checkedAt"`
	Destination  string    `json:"destination"`
	Registration string    `json:"registration"`
	Files        string    `json:"files"`
	SetupOutput  string    `json:"setupOutput"`
	Message      string    `json:"message"`
}

func (value BootstrapRecovery) Validate() error {
	if err := value.ID.Validate(); err != nil {
		return err
	}
	if err := value.ProjectID.Validate(); err != nil {
		return err
	}
	if err := validatePath(value.Path); err != nil {
		return err
	}
	if len(value.Path) > 4096 {
		return errors.New("recovery path exceeds limit")
	}
	if !slices.Contains([]string{"publication", "preparation", "git", "environment", "dependencies"}, value.Kind) {
		return errors.New("invalid recovery kind")
	}
	if err := validateTime(value.RecordedAt); err != nil {
		return err
	}
	if value.Kind == "publication" {
		if value.Project == nil || value.Project.ID != value.ProjectID || value.Project.Path != value.Path || value.Project.Source != SourceBootstrapped {
			return errors.New("invalid recovery registration")
		}
		if err := value.Project.Validate(); err != nil {
			return err
		}
		for _, command := range value.Project.Commands {
			if command.Approval != nil {
				return errors.New("recovery must not store execution approval")
			}
		}
		if len(value.Files) == 0 || len(value.Files) > 64 {
			return errors.New("invalid recovery manifest size")
		}
		seen := map[string]bool{}
		total := int64(0)
		for _, file := range value.Files {
			if !fs.ValidPath(file.Path) || file.Path == "." || strings.ContainsRune(file.Path, 0) || len(file.Path) > 1024 || len(strings.Split(file.Path, "/")) > 8 || seen[file.Path] || file.Size < 0 || file.Size > 1<<20 {
				return errors.New("invalid recovery file")
			}
			seen[file.Path] = true
			total += file.Size
			hash, err := hex.DecodeString(file.SHA256)
			if err != nil || len(hash) != 32 || strings.ToLower(file.SHA256) != file.SHA256 {
				return errors.New("invalid file hash")
			}
		}
		if total > 1<<20 {
			return errors.New("recovery manifest exceeds byte limit")
		}
		for name := range seen {
			for parent := filepath.ToSlash(filepath.Dir(name)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
				if seen[parent] {
					return errors.New("manifest file/directory collision")
				}
			}
		}
	} else if value.Project != nil || len(value.Files) != 0 {
		return errors.New("non-publication recovery must not store a registration")
	}
	if value.Assessment != nil {
		return value.Assessment.Validate()
	}
	return nil
}
func (value RecoveryAssessment) Validate() error {
	if err := validateTime(value.CheckedAt); err != nil {
		return err
	}
	if !slices.Contains([]string{"missing", "directory", "unsafe"}, value.Destination) || !slices.Contains([]string{"missing", "matching", "conflict"}, value.Registration) || !slices.Contains([]string{"not_checked", "matching", "changed"}, value.Files) || !slices.Contains([]string{"not_applicable", "absent", "present", "unsafe"}, value.SetupOutput) {
		return errors.New("invalid recovery observation")
	}
	return validateText(value.Message, 1024)
}
