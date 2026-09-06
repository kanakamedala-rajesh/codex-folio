//go:build windows

package codex

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func codexCommand(ctx context.Context, executable string, args ...string) *exec.Cmd {
	extension := strings.ToLower(filepath.Ext(executable))
	if extension == ".cmd" || extension == ".bat" {
		command := exec.CommandContext(ctx, "cmd.exe")
		command.Args = nil
		command.SysProcAttr = &syscall.SysProcAttr{CmdLine: batchCommandLine(executable, args)}
		return command
	}
	return exec.CommandContext(ctx, executable, args...)
}

func sameEnvironmentName(left, right string) bool { return strings.EqualFold(left, right) }

func batchCommandLine(executable string, args []string) string {
	parts := []string{quoteBatchArgument(executable)}
	for _, arg := range args {
		parts = append(parts, quoteBatchArgument(arg))
	}
	return `/d /v:off /s /c "` + strings.Join(parts, " ") + `"`
}

func quoteBatchArgument(value string) string {
	value = strings.ReplaceAll(value, "^", "^^")
	value = strings.ReplaceAll(value, "%", "%%")
	value = strings.ReplaceAll(value, `"`, `^"`)
	return `"` + value + `"`
}
