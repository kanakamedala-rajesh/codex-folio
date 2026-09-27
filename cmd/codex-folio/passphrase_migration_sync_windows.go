//go:build windows

package main

// Windows does not support syncing directory handles.
func syncMigrationDirectory(string) error { return nil }
