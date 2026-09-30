package main

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/launch"
)

// Codex places a daemon socket below its canonical home on Unix. Leave room
// for that socket even when the caller supplied a shorter symlink to the home.
const maxDaemonSafeHomeBytes = 64

func foregroundCodexArguments(plan launch.Plan, goos string, supportsNoDaemon func(string) bool) ([]string, error) {
	arguments := append([]string(nil), plan.Arguments...)
	if goos == "windows" || strings.TrimSpace(plan.Environment["CODEX_HOME"]) == "" {
		return arguments, nil
	}
	home, err := filepath.EvalSymlinks(plan.Environment["CODEX_HOME"])
	if err != nil {
		return nil, apperrors.New(apperrors.LaunchProcessStartFailed, err)
	}
	if len([]byte(home)) <= maxDaemonSafeHomeBytes || hasNoDaemonArgument(arguments) {
		return arguments, nil
	}
	if !supportsNoDaemon(plan.Executable) {
		return nil, apperrors.New(apperrors.LaunchDaemonUnsupported, errors.New("installed Codex does not advertise --no-daemon"))
	}
	return append([]string{"--no-daemon"}, arguments...), nil
}

func hasNoDaemonArgument(arguments []string) bool {
	for _, argument := range arguments {
		if argument == "--" {
			break
		}
		if argument == "--no-daemon" {
			return true
		}
	}
	return false
}

func codexSupportsNoDaemon(executable string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, executable, "--help").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "--no-daemon" {
			return true
		}
	}
	return false
}
