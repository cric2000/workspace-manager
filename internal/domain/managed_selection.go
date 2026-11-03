package domain

// ManagedSelection preserves known launch metadata across controller restarts.
// It is separate from immutable user session snapshots and process ownership.
type ManagedSelection struct {
	ID              ID      `json:"id"`
	OriginSessionID ID      `json:"originSessionId,omitempty"`
	Snapshot        Session `json:"snapshot"`
}

func (s ManagedSelection) Validate() error {
	if err := s.ID.Validate(); err != nil {
		return err
	}
	if err := validateOptionalID(s.OriginSessionID); err != nil {
		return err
	}
	return s.Snapshot.Validate()
}
