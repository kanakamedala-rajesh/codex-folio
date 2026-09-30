//go:build windows

package main

import (
	"context"
	"errors"
	"os"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/vault"
)

func openServiceStore(paths platform.Paths) (*store.Store, error) {
	return openServiceStoreWithVaultMode(paths, "", "")
}

func openServiceStoreWithVaultMode(paths platform.Paths, mode platform.VaultMode, passphrase string) (*store.Store, error) {
	switch mode {
	case "", platform.VaultModeSecretService:
		secureVault, err := newNativeServiceVault(paths, mode, allowVaultInitialization(paths.DatabaseFile))
		if err != nil {
			return nil, err
		}
		return store.OpenWithVault(paths.DatabaseFile, secureVault)
	case platform.VaultModePassphrase:
		secureVault, err := platform.NewPassphraseVaultWithOptions(paths.VaultFile, platform.PassphraseOptions{
			AllowCreate: allowVaultInitialization(paths.DatabaseFile),
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
		return nil, apperrors.New(apperrors.VaultUnavailable, errors.New("selected vault mode is unavailable on Windows"))
	}
}

func newNativeServiceVault(paths platform.Paths, mode platform.VaultMode, allowCreate bool) (vault.Vault, error) {
	if mode != "" && mode != platform.VaultModeSecretService {
		return nil, apperrors.New(apperrors.VaultUnavailable, errors.New("unsupported native vault mode"))
	}
	vaultPath := paths.VaultFile
	if allowCreate && existingPassphraseInstallation(paths) {
		vaultPath = nativeMigrationVaultPath(paths)
	}
	return platform.NewDPAPIVaultWithOptions(vaultPath, platform.DPAPIOptions{AllowCreate: allowCreate})
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
