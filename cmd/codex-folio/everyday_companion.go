package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
)

const companionStartupTimeout = 5 * time.Second

type companionProcessStarter func(platform.Paths, serviceOptions) error

func startDetachedCompanion(paths platform.Paths, options serviceOptions) error {
	executable, err := os.Executable()
	if err != nil {
		return apperrors.New(apperrors.PlatformServiceUnavailable, err)
	}
	args := []string{"service", "start", "--state-root", paths.Root}
	if options.vaultMode != "" {
		args = append(args, "--vault-mode", string(options.vaultMode))
	}
	command := detachedCompanionCommand(executable, args...)
	input, err := os.Open(os.DevNull)
	if err != nil {
		return apperrors.New(apperrors.PlatformServiceUnavailable, err)
	}
	defer input.Close()
	output, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return apperrors.New(apperrors.PlatformServiceUnavailable, err)
	}
	defer output.Close()
	command.Stdin = input
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		return apperrors.New(apperrors.PlatformServiceUnavailable, err)
	}
	if command.Process == nil {
		return apperrors.New(apperrors.PlatformServiceUnavailable, errors.New("companion process did not start"))
	}
	if err := command.Process.Release(); err != nil {
		return apperrors.New(apperrors.PlatformServiceUnavailable, err)
	}
	return nil
}

func ensureEverydayCompanion(paths platform.Paths, options serviceOptions, input io.Reader, stdout, stderr io.Writer, start companionProcessStarter) int {
	status, err := platform.Discover(paths, platform.OwnerOptions{})
	if err != nil {
		// An owner publishes its authenticated descriptor immediately after
		// acquiring the lock. A concurrent plain start can observe that narrow
		// interval; reuse it if publication completes, while retaining the
		// original fail-closed metadata error when it does not.
		if apperrors.Code(err) != apperrors.PlatformServiceMetadataInvalid {
			return writeServiceError(stderr, err)
		}
		if connection, waitErr := waitForCompanionClient(paths, time.Second); waitErr == nil {
			return useEverydayCompanion(connection, options, input, stdout, stderr)
		}
		return writeServiceError(stderr, err)
	}
	if !status.Running {
		if start == nil {
			return writeServiceError(stderr, apperrors.New(apperrors.PlatformServiceUnavailable, errors.New("companion starter is unavailable")))
		}
		if err := start(paths, options); err != nil {
			return writeServiceError(stderr, err)
		}
	}

	connection, err := waitForCompanionClient(paths, companionStartupTimeout)
	if err != nil {
		return writeServiceError(stderr, err)
	}
	return useEverydayCompanion(connection, options, input, stdout, stderr)
}

func useEverydayCompanion(connection platform.ServiceClient, options serviceOptions, input io.Reader, stdout, stderr io.Writer) int {
	client := httpapi.NewCommandClient(connection.Origin, connection.Token, nil)
	health, err := client.ServiceHealth(context.Background())
	if err != nil {
		return writeServiceError(stderr, err)
	}
	if health.ServiceState == httpapi.ServiceStateLocked && options.vaultMode == platform.VaultModePassphrase {
		passphrase, readErr := readVaultUnlockPassphrase(companionPassphraseInput(input), stderr)
		if readErr != nil {
			return writeServiceError(stderr, readErr)
		}
		health, err = client.UnlockVault(context.Background(), passphrase)
		passphrase = ""
		if err != nil {
			return writeServiceError(stderr, err)
		}
	}
	if health.ServiceState != httpapi.ServiceStateReady || health.VaultState != httpapi.VaultStateUnlocked || health.DatabaseState != httpapi.DatabaseStateReady {
		code := health.ErrorCode
		if code == "" {
			code = apperrors.HTTPAPIServiceUnavailable
		}
		return writeServiceError(stderr, apperrors.New(code, errors.New("companion state is not ready for launch")))
	}

	dashboardAddress := nonSecretDashboardAddress(connection.Origin)
	if _, err := client.Dashboard(context.Background()); err != nil {
		_, _ = fmt.Fprintln(stderr, "codex-folio: warning: dashboard authorization is temporarily unavailable; foreground launch remains available")
	}
	_, _ = fmt.Fprintf(stdout, "dashboard address: %s\n", dashboardAddress)
	_, _ = fmt.Fprintf(stdout, "reopen with: %s\n", companionReopenCommand(options))
	return exitSuccess
}

func companionPassphraseInput(input io.Reader) io.Reader {
	return companionPassphraseInputWithTerminalCheck(input, term.IsTerminal)
}

func companionPassphraseInputWithTerminalCheck(input io.Reader, isTerminal func(int) bool) io.Reader {
	promptInput, ok := input.(*foregroundPromptInput)
	if !ok || promptInput.terminal == nil || !isTerminal(int(promptInput.terminal.Fd())) {
		return input
	}
	return promptInput.terminal
}

func waitForCompanionClient(paths platform.Paths, timeout time.Duration) (platform.ServiceClient, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		connection, err := platform.DiscoverServiceClient(paths, platform.OwnerOptions{})
		if err == nil {
			return connection, nil
		}
		lastErr = err
		if !time.Now().Before(deadline) {
			return platform.ServiceClient{}, apperrors.New(apperrors.HTTPAPIServiceUnavailable, lastErr)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func nonSecretDashboardAddress(origin string) string {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(origin, "/") + "/"
	}
	parsed.Path = "/"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func companionReopenCommand(options serviceOptions) string {
	parts := []string{"codex-folio", "service", "start"}
	if options.stateRoot != nil {
		parts = append(parts, "--state-root", displayCommandArgument(*options.stateRoot))
	}
	if options.vaultMode != "" {
		parts = append(parts, "--vault-mode", string(options.vaultMode))
	}
	return strings.Join(parts, " ")
}

func displayCommandArgument(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\r\n\"'") {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
