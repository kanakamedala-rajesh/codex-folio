//go:build !windows

package main

import (
	"errors"
	"os"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

func syncEverydayStorageDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return apperrors.New(apperrors.VaultUnavailable, err)
	}
	if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
		return apperrors.New(apperrors.VaultUnavailable, err)
	}
	return nil
}
