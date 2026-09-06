//go:build !windows

package main

import "os/exec"

func foregroundCommand(executable string, args ...string) *exec.Cmd {
	return exec.Command(executable, args...)
}
