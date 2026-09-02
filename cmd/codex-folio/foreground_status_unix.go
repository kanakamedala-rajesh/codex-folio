//go:build !windows

package main

import (
	"os"
	"syscall"
)

func foregroundExitStatus(state *os.ProcessState) int {
	if state == nil {
		return -1
	}
	if status := state.ExitCode(); status >= 0 {
		return status
	}
	if waitStatus, ok := state.Sys().(syscall.WaitStatus); ok && waitStatus.Signaled() {
		return 128 + int(waitStatus.Signal())
	}
	return -1
}
