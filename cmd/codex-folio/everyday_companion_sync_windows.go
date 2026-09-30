//go:build windows

package main

// Windows does not support syncing directory handles.
func syncEverydayStorageDirectory(string) error { return nil }
