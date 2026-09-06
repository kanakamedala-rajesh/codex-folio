//go:build !windows

package codex

import (
	"context"
	"os/exec"
)

func codexCommand(ctx context.Context, executable string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, executable, args...)
}

func sameEnvironmentName(left, right string) bool { return left == right }
