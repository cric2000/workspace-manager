package storage

import (
	"context"
	"workspace-manager/internal/domain"
)

// UpdateRuntime replaces disposable supervisor status without creating a backup
// on every log tick. Durable user snapshots continue to use revisioned updates.
func UpdateRuntime(ctx context.Context, repository *Repository[domain.ManagedRun], value domain.ManagedRun, expected Revision) error {
	name, err := entityFilename(value.ID)
	if err != nil {
		return err
	}
	return repository.store.transaction(ctx, func() error {
		current, _, err := repository.store.read(name)
		if err != nil {
			return err
		}
		if expected != current.Revision {
			return ErrConflict
		}
		if err := stableIdentity(current.Value, value); err != nil {
			return err
		}
		data, err := repository.store.codec.Encode(value)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return repository.store.writePrimary(name, data)
	})
}
