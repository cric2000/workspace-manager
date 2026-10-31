// Package domain defines records independently of Wails and Linux side effects.
package domain

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// ID is an opaque 128-bit identity encoded as 32 lowercase hexadecimal digits.
// Names are editable; IDs remain stable and are safe as single path components.
type ID string

func NewID() (ID, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return ID(hex.EncodeToString(value[:])), nil
}

func (id ID) Validate() error {
	if len(id) != 32 {
		return errors.New("ID must contain 32 lowercase hexadecimal digits")
	}
	for _, character := range id {
		isDigit := character >= '0' && character <= '9'
		isLowerHex := character >= 'a' && character <= 'f'
		if !isDigit && !isLowerHex {
			return errors.New("ID must contain 32 lowercase hexadecimal digits")
		}
	}
	return nil
}
