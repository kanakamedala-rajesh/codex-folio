//go:build darwin

package main

import (
	"errors"
	"os"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

func openServiceStore(paths platform.Paths) (*store.Store, error) {
	return openServiceStoreWithVaultMode(paths, "", "")
}

func openServiceStoreWithVaultMode(paths platform.Paths, mode platform.VaultMode, _ string) (*store.Store, error) {
	if mode != "" {
		return nil, apperrors.New(apperrors.VaultUnavailable, errors.New("selected vault mode is unavailable on macOS"))
	}
	secureVault, err := platform.NewKeychainVaultWithOptions(platform.KeychainOptions{
		AllowCreate: allowKeychainInitialization(paths.DatabaseFile),
	})
	if err != nil {
		return nil, err
	}
	return store.OpenWithVault(paths.DatabaseFile, secureVault)
}

func allowKeychainInitialization(databasePath string) bool {
	info, err := os.Lstat(databasePath)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	return !info.Mode().IsRegular() || info.Size() == 0
}
