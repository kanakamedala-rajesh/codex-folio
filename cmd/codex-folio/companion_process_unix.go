//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func detachedCompanionCommand(executable string, args ...string) *exec.Cmd {
	command := exec.Command(executable, args...)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return command
}
