//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"

	"venkatasudha.com/codex-folio/internal/platform"
)

const passphraseMigrationVaultBackupName = "codex-folio.passphrase.vault"

func nativeMigrationVaultPath(paths platform.Paths) string {
	return paths.VaultFile + ".native-migration"
}

func archivedPassphraseVaultPath(paths platform.Paths) string {
	return filepath.Join(passphraseMigrationBackupDirectory(paths), passphraseMigrationVaultBackupName)
}

func commitNativeVaultMigration(paths platform.Paths, target platform.VaultMode) error {
	if target != platform.VaultModeSecretService {
		return nil
	}
	temporary := nativeMigrationVaultPath(paths)
	if _, err := safeRegularMigrationFile(temporary); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return migrationRequiredError(err)
	}
	archived := archivedPassphraseVaultPath(paths)
	if _, err := safeRegularMigrationFile(archived); errors.Is(err, os.ErrNotExist) {
		if _, sourceErr := safeRegularMigrationFile(paths.VaultFile); sourceErr != nil {
			return migrationRequiredError(sourceErr)
		}
		if renameErr := os.Rename(paths.VaultFile, archived); renameErr != nil {
			return migrationRequiredError(renameErr)
		}
	} else if err != nil {
		return migrationRequiredError(err)
	}
	if _, err := os.Lstat(paths.VaultFile); err == nil {
		return migrationRequiredError(errors.New("native vault promotion found an unexpected destination file"))
	} else if !errors.Is(err, os.ErrNotExist) {
		return migrationRequiredError(err)
	}
	if err := os.Rename(temporary, paths.VaultFile); err != nil {
		return migrationRequiredError(err)
	}
	return nil
}

func restoreNativeVaultMigration(paths platform.Paths) error {
	temporary := nativeMigrationVaultPath(paths)
	if _, err := safeRegularMigrationFile(temporary); err == nil {
		if err := os.Remove(temporary); err != nil {
			return migrationRequiredError(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return migrationRequiredError(err)
	}
	archived := archivedPassphraseVaultPath(paths)
	if _, err := safeRegularMigrationFile(archived); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return migrationRequiredError(err)
	}
	if _, err := safeRegularMigrationFile(paths.VaultFile); err == nil {
		if err := os.Remove(paths.VaultFile); err != nil {
			return migrationRequiredError(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return migrationRequiredError(err)
	}
	if err := os.Rename(archived, paths.VaultFile); err != nil {
		return migrationRequiredError(err)
	}
	return nil
}

func safeRegularMigrationFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("secure-storage migration vault path is unsafe")
	}
	return info, nil
}
