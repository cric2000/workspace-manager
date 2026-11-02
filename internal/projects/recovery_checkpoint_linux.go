package projects

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/storage"
)

func openRecoveryRecords(paths storage.Paths) (*storage.Repository[domain.BootstrapRecovery], error) {
	if !filepath.IsAbs(paths.StateDir) {
		return nil, storage.ErrInvalidDocument
	}
	codec, err := storage.NewCodec[domain.BootstrapRecovery](1, nil)
	if err != nil {
		return nil, err
	}
	return storage.OpenRepository(filepath.Join(paths.BootstrapRecoveryDir(), "records"), codec)
}
func recoveryLease(paths storage.Paths, id domain.ID) (*os.File, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(paths.StateDir) {
		return nil, storage.ErrInvalidDocument
	}
	root, err := storage.OpenPrivateDirectory(filepath.Join(paths.BootstrapRecoveryDir(), "leases", string(id)))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return storage.TryDirectoryLock(root)
}
func startRecoveryCheckpoint(ctx context.Context, paths storage.Paths, seed domain.BootstrapRecovery) (*os.File, error) {
	if err := seed.Validate(); err != nil {
		return nil, err
	}
	lease, err := recoveryLease(paths, seed.ID)
	if err != nil {
		return nil, err
	}
	repository, err := openRecoveryRecords(paths)
	if err != nil {
		lease.Close()
		return nil, err
	}
	defer repository.Close()
	if err := repository.Create(ctx, seed); err != nil {
		lease.Close()
		return nil, err
	}
	return lease, nil
}

type leasedCreationLog struct {
	creationLogWriter
	lease *os.File
}

func (log leasedCreationLog) Close() error {
	return errors.Join(log.creationLogWriter.Close(), log.lease.Close())
}
