//go:build windows

package main

import (
	"errors"
	"os"

	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

func openServiceStore(paths platform.Paths) (*store.Store, error) {
	secureVault, err := platform.NewDPAPIVaultWithOptions(paths.VaultFile, platform.DPAPIOptions{
		AllowCreate: allowDPAPIInitialization(paths.DatabaseFile),
	})
	if err != nil {
		return nil, err
	}
	return store.OpenWithVault(paths.DatabaseFile, secureVault)
}

func allowDPAPIInitialization(databasePath string) bool {
	info, err := os.Lstat(databasePath)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	return !info.Mode().IsRegular() || info.Size() == 0
}
