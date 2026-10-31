package domain

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func validateName(name string) error {
	if !utf8.ValidString(name) || name == "" || name != strings.TrimSpace(name) || utf8.RuneCountInString(name) > 120 {
		return errors.New("name must have 1–120 characters with no surrounding whitespace")
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return errors.New("name must not contain control characters")
		}
	}
	return nil
}

func validateText(value string, limit int) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || utf8.RuneCountInString(value) > limit {
		return fmt.Errorf("text must be valid UTF-8, contain no NUL, and have at most %d characters", limit)
	}
	return nil
}

func validatePath(path string) error {
	if !utf8.ValidString(path) || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("path must be absolute, clean UTF-8 with no NUL")
	}
	return nil
}

func validateTimes(created, updated time.Time) error {
	if err := validateTime(created); err != nil {
		return fmt.Errorf("createdAt: %w", err)
	}
	if err := validateTime(updated); err != nil {
		return fmt.Errorf("updatedAt: %w", err)
	}
	if updated.Before(created) {
		return errors.New("updatedAt must not precede createdAt")
	}
	return nil
}

func validateTime(value time.Time) error {
	if value.IsZero() || value.Year() < 1 || value.Year() > 9999 {
		return errors.New("timestamp must be nonzero and representable in JSON")
	}
	// MarshalJSON also rejects timezone offsets outside RFC 3339's range.
	if _, err := value.MarshalJSON(); err != nil {
		return errors.New("timestamp must be representable in JSON")
	}
	return nil
}

func validateOptionalID(id ID) error {
	if id == "" {
		return nil
	}
	return id.Validate()
}
