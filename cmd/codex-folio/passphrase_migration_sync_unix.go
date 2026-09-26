//go:build !windows

package main

import (
	"errors"
	"os"
)

func syncMigrationDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return migrationRequiredError(err)
	}
	if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
		return migrationRequiredError(err)
	}
	return nil
}
