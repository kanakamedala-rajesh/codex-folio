package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/platform"
)

const (
	passphraseMigrationVersion  = 1
	passphraseMigrationFileName = "secure-storage-migration.json"
)

type passphraseMigrationRecord struct {
	Version       int                `json:"version"`
	Target        platform.VaultMode `json:"target"`
	BackupID      string             `json:"backup_id"`
	DatabaseReady bool               `json:"database_ready"`
}

func passphraseMigrationPath(paths platform.Paths) string {
	return filepath.Join(paths.Root, passphraseMigrationFileName)
}

func readPassphraseMigrationRecord(paths platform.Paths) (passphraseMigrationRecord, bool, error) {
	path := passphraseMigrationPath(paths)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return passphraseMigrationRecord{}, false, nil
	}
	if err != nil {
		return passphraseMigrationRecord{}, false, migrationRequiredError(err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return passphraseMigrationRecord{}, false, migrationRequiredError(errors.New("secure-storage migration record path is unsafe"))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return passphraseMigrationRecord{}, false, migrationRequiredError(err)
	}
	var record passphraseMigrationRecord
	if json.Unmarshal(data, &record) != nil || record.Version != passphraseMigrationVersion ||
		(record.Target != platform.VaultModeSecretService && record.Target != platform.VaultModeWSLDPAPI) ||
		!strings.HasPrefix(record.BackupID, "backup-") {
		return passphraseMigrationRecord{}, false, migrationRequiredError(errors.New("secure-storage migration record is invalid"))
	}
	return record, true, nil
}

func writePassphraseMigrationRecord(paths platform.Paths, record passphraseMigrationRecord) error {
	record.Version = passphraseMigrationVersion
	encoded, err := json.Marshal(record)
	if err != nil {
		return migrationRequiredError(err)
	}
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		return migrationRequiredError(err)
	}
	temporary, err := os.CreateTemp(paths.Root, ".secure-storage-migration-*.tmp")
	if err != nil {
		return migrationRequiredError(err)
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return migrationRequiredError(err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return migrationRequiredError(err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return migrationRequiredError(err)
	}
	if err := temporary.Close(); err != nil {
		return migrationRequiredError(err)
	}
	if err := os.Rename(name, passphraseMigrationPath(paths)); err != nil {
		return migrationRequiredError(err)
	}
	return syncMigrationDirectory(paths.Root)
}

func removePassphraseMigrationRecord(paths platform.Paths) error {
	path := passphraseMigrationPath(paths)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return migrationRequiredError(errors.New("secure-storage migration record path is unsafe"))
	}
	if err := os.Remove(path); err != nil {
		return migrationRequiredError(err)
	}
	return syncMigrationDirectory(paths.Root)
}

func migrationRequiredError(cause error) error {
	return apperrors.New(apperrors.VaultMigrationRequired, cause)
}
