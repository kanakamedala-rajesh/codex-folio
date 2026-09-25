//go:build windows

package main

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

const userEnvironmentKey = `Environment`

func windowsCommandPathContains(directory string) (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, userEnvironmentKey, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer key.Close()
	value, _, err := key.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	return windowsPathContains(value, directory), err
}

func addWindowsCommandPath(directory string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, userEnvironmentKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	value, valueType, err := key.GetStringValue("Path")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	if windowsPathContains(value, directory) {
		return nil
	}
	if valueType != 0 && valueType != registry.SZ && valueType != registry.EXPAND_SZ {
		return errors.New("user Path has an unsupported registry type")
	}
	value = appendWindowsPath(value, directory)
	if valueType == registry.EXPAND_SZ {
		return key.SetExpandStringValue("Path", value)
	}
	return key.SetStringValue("Path", value)
}
