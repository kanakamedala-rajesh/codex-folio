//go:build windows

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func foregroundCommand(executable string, args ...string) *exec.Cmd {
	extension := strings.ToLower(filepath.Ext(executable))
	if extension == ".cmd" || extension == ".bat" {
		command := exec.Command("cmd.exe")
		command.Args = nil
		command.SysProcAttr = &syscall.SysProcAttr{CmdLine: foregroundBatchCommandLine(executable, args)}
		return command
	}
	return exec.Command(executable, args...)
}

func foregroundBatchCommandLine(executable string, args []string) string {
	parts := []string{quoteForegroundBatchArgument(executable, true)}
	for _, arg := range args {
		parts = append(parts, quoteForegroundBatchArgument(arg, false))
	}
	return `cmd.exe /d /v:off /s /c "` + strings.Join(parts, " ") + `"`
}

func quoteForegroundBatchArgument(value string, always bool) string {
	value = strings.ReplaceAll(value, "^", "^^")
	value = strings.ReplaceAll(value, "%", "%%")
	value = strings.ReplaceAll(value, `"`, `^"`)
	if !always && value != "" && !strings.ContainsAny(value, " \t&|<>()") {
		return value
	}
	return `"` + value + `"`
}
