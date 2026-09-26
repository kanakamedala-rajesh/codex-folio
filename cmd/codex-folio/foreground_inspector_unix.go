//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

type foregroundProcessInspector struct{}

func (foregroundProcessInspector) IsRunning(processID int) (bool, error) {
	if processID <= 0 {
		return false, nil
	}
	process, err := os.FindProcess(processID)
	if err != nil {
		return false, nil
	}
	if err := process.Signal(syscall.Signal(0)); err == nil {
		return true, nil
	} else if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
		return false, nil
	} else if errors.Is(err, syscall.EPERM) {
		return true, nil
	} else {
		return false, err
	}
}
