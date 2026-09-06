//go:build windows

package main

import (
	"errors"

	"golang.org/x/sys/windows"
)

const windowsStillActive = 259

type foregroundProcessInspector struct{}

func (foregroundProcessInspector) IsRunning(processID int) (bool, error) {
	if processID <= 0 {
		return false, nil
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(processID))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false, nil
	}
	if err != nil {
		return true, nil
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var status uint32
	if err := windows.GetExitCodeProcess(handle, &status); err != nil {
		return false, err
	}
	return status == windowsStillActive, nil
}
