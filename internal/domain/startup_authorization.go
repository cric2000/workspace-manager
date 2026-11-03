package domain

import (
	"errors"
	"time"
)

// StartupAuthorization binds unattended login to an explicitly reviewed plan.
// Changes to settings, application metadata or commands require another review.
type StartupAuthorization struct {
	ID          ID        `json:"id"`
	Fingerprint string    `json:"fingerprint"`
	ApprovedAt  time.Time `json:"approvedAt"`
}

func (a StartupAuthorization) Validate() error {
	if a.ID.Validate() != nil || len(a.Fingerprint) != 64 {
		return errors.New("invalid startup authorization")
	}
	for _, c := range a.Fingerprint {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return errors.New("invalid fingerprint")
		}
	}
	return validateTime(a.ApprovedAt)
}
