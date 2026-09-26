//go:build windows

package main

// Passphrase migration is Linux-only; Windows does not support syncing directory handles.
func syncMigrationDirectory(string) error { return nil }
