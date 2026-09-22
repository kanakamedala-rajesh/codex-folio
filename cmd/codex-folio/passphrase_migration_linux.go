//go:build linux

package main

import (
	"context"
	"errors"

	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/vault"
)

type nativeVaultFactory func(platform.Paths, platform.VaultMode, bool) (vault.Vault, error)

func migrateOpenedPassphraseStore(paths platform.Paths, target platform.VaultMode, sourceStore *store.Store) (*store.Store, error) {
	return migrateOpenedPassphraseStoreWithVaultFactory(paths, target, sourceStore, newNativeServiceVault)
}

func migrateOpenedPassphraseStoreWithVaultFactory(paths platform.Paths, target platform.VaultMode, sourceStore *store.Store, newDestination nativeVaultFactory) (*store.Store, error) {
	if target != platform.VaultModeSecretService && target != platform.VaultModeWSLDPAPI {
		return nil, migrationRequiredError(errors.New("secure-storage migration target is unsupported"))
	}
	if sourceStore == nil {
		return nil, migrationRequiredError(errors.New("passphrase source store is unavailable"))
	}
	closed := false
	closeSource := func() error {
		if closed {
			return nil
		}
		closed = true
		return sourceStore.Close()
	}
	defer closeSource()
	if err := sourceStore.VerifyProtectedState(context.Background()); err != nil {
		return nil, err
	}
	backup, err := sourceStore.CreateVaultMigrationBackup(context.Background())
	if err != nil {
		return nil, err
	}
	destinationVault, err := newDestination(paths, target, true)
	if err != nil {
		return nil, err
	}
	record := passphraseMigrationRecord{Target: target, BackupID: backup.ID}
	if err := writePassphraseMigrationRecord(paths, record); err != nil {
		return nil, err
	}
	if err := sourceStore.ReprotectVaultState(context.Background(), destinationVault); err != nil {
		_ = removePassphraseMigrationRecord(paths)
		return nil, migrationRequiredError(errors.Join(err, errors.New("the original passphrase-protected database remains active")))
	}
	record.DatabaseReady = true
	if err := writePassphraseMigrationRecord(paths, record); err != nil {
		_ = closeSource()
		return nil, rollbackPassphraseMigration(paths, backup.ID, err)
	}
	if err := closeSource(); err != nil {
		return nil, rollbackPassphraseMigration(paths, backup.ID, err)
	}
	destinationStore, err := openVerifiedMigratedDestination(paths, target, newDestination)
	if err != nil {
		return nil, rollbackPassphraseMigration(paths, backup.ID, err)
	}
	if err := rememberEverydaySecureStorage(paths, target); err != nil {
		_ = destinationStore.Close()
		return nil, rollbackPassphraseMigration(paths, backup.ID, err)
	}
	if err := removePassphraseMigrationRecord(paths); err != nil {
		_ = destinationStore.Close()
		return nil, err
	}
	return destinationStore, nil
}

func resumeInterruptedPassphraseMigration(paths platform.Paths, target platform.VaultMode) (bool, error) {
	return resumeInterruptedPassphraseMigrationWithVaultFactory(paths, target, newNativeServiceVault)
}

func resumeInterruptedPassphraseMigrationWithVaultFactory(paths platform.Paths, target platform.VaultMode, newDestination nativeVaultFactory) (bool, error) {
	record, exists, err := readPassphraseMigrationRecord(paths)
	if err != nil || !exists {
		return false, err
	}
	if record.Target != target {
		return false, migrationRequiredError(errors.New("secure-storage migration target changed; restore the original platform prerequisites"))
	}
	if record.DatabaseReady {
		if destinationStore, openErr := openVerifiedMigratedDestination(paths, target, newDestination); openErr == nil {
			_ = destinationStore.Close()
			if err := rememberEverydaySecureStorage(paths, target); err != nil {
				return false, err
			}
			if err := removePassphraseMigrationRecord(paths); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	if err := restorePassphraseMigrationBackup(paths, record.BackupID); err != nil {
		return false, migrationRequiredError(errors.Join(errors.New("interrupted secure-storage migration requires explicit database recovery"), err))
	}
	if err := rememberEverydaySecureStorage(paths, platform.VaultModePassphrase); err != nil {
		return false, err
	}
	if err := removePassphraseMigrationRecord(paths); err != nil {
		return false, err
	}
	return false, nil
}

func openVerifiedMigratedDestination(paths platform.Paths, target platform.VaultMode, newDestination nativeVaultFactory) (*store.Store, error) {
	destinationVault, err := newDestination(paths, target, false)
	if err != nil {
		return nil, err
	}
	destinationStore, err := store.OpenWithVault(paths.DatabaseFile, destinationVault)
	if err != nil {
		return nil, err
	}
	if err := destinationStore.VerifyIntegrity(); err != nil {
		_ = destinationStore.Close()
		return nil, err
	}
	if err := destinationStore.VerifyProtectedState(context.Background()); err != nil {
		_ = destinationStore.Close()
		return nil, err
	}
	return destinationStore, nil
}

func rollbackPassphraseMigration(paths platform.Paths, backupID string, cause error) error {
	if restoreErr := restorePassphraseMigrationBackup(paths, backupID); restoreErr != nil {
		return migrationRequiredError(errors.Join(cause, errors.New("destination verification failed and automatic restoration also failed"), restoreErr))
	}
	if selectionErr := rememberEverydaySecureStorage(paths, platform.VaultModePassphrase); selectionErr != nil {
		return migrationRequiredError(errors.Join(cause, errors.New("the original passphrase-protected database was restored"), selectionErr))
	}
	if removeErr := removePassphraseMigrationRecord(paths); removeErr != nil {
		return migrationRequiredError(errors.Join(cause, errors.New("the original passphrase-protected database was restored"), removeErr))
	}
	return migrationRequiredError(errors.Join(cause, errors.New("the original passphrase-protected database was restored")))
}

func restorePassphraseMigrationBackup(paths platform.Paths, backupID string) error {
	recovery, err := store.NewRecovery(store.RecoveryOptions{DatabasePath: paths.DatabaseFile})
	if err != nil {
		return err
	}
	_, err = recovery.Restore(context.Background(), backupID)
	return err
}
