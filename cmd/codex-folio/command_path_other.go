//go:build !windows

package main

import "errors"

func windowsCommandPathContains(string) (bool, error) {
	return false, errors.New("Windows user PATH is unavailable on this platform")
}

func addWindowsCommandPath(string) error {
	return errors.New("Windows user PATH is unavailable on this platform")
}
