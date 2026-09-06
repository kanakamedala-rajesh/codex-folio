//go:build windows

package main

import "os"

func foregroundExitStatus(state *os.ProcessState) int {
	if state == nil {
		return -1
	}
	return state.ExitCode()
}
