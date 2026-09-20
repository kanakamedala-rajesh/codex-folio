//go:build !windows && !darwin

package main

import (
	"context"
	"errors"
	"os"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

func openServiceStore(paths platform.Paths) (*store.Store, error) {
	return openServiceStoreWithVaultMode(paths, platform.VaultModeSecretService, "")
}

func openServiceStoreWithVaultMode(paths platform.Paths, mode platform.VaultMode, passphrase string) (*store.Store, error) {
	switch mode {
	case "", platform.VaultModeSecretService:
		secureVault, err := platform.NewSecretServiceVaultWithOptions(platform.SecretServiceOptions{
			AllowCreate: allowSecretServiceInitialization(paths.DatabaseFile),
		})
		if err != nil {
			return nil, err
		}
		return store.OpenWithVault(paths.DatabaseFile, secureVault)
	case platform.VaultModePassphrase:
		secureVault, err := platform.NewPassphraseVaultWithOptions(paths.VaultFile, platform.PassphraseOptions{
			AllowCreate: allowPassphraseInitialization(paths.DatabaseFile),
		})
		if err != nil {
			return nil, err
		}
		if err := secureVault.Unlock(context.Background(), passphrase); err != nil {
			return nil, err
		}
		stateStore, err := store.OpenWithVault(paths.DatabaseFile, secureVault)
		if err != nil {
			secureVault.Lock()
			return nil, err
		}
		return stateStore, nil
	default:
		return nil, apperrors.New(apperrors.VaultUnavailable, errors.New("unsupported vault mode"))
	}
}

func allowSecretServiceInitialization(databasePath string) bool {
	return allowVaultInitialization(databasePath)
}

func allowPassphraseInitialization(databasePath string) bool {
	return allowVaultInitialization(databasePath)
}

func allowVaultInitialization(databasePath string) bool {
	info, err := os.Lstat(databasePath)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	return !info.Mode().IsRegular() || info.Size() == 0
}
