//go:build windows || darwin

package main

import (
	"errors"

	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

func migrateOpenedPassphraseStore(platform.Paths, platform.VaultMode, *store.Store) (*store.Store, error) {
	return nil, migrationRequiredError(errors.New("passphrase installation migration is supported only for existing Linux or WSL state"))
}

func resumeInterruptedPassphraseMigration(platform.Paths, platform.VaultMode) (bool, error) {
	return false, nil
}
