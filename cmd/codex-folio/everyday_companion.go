package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
)

const companionStartupTimeout = 5 * time.Second

const (
	companionStartupReady         = "READY"
	secureStorageSelectionVersion = 1
)

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
	if options.startupStatus != "" {
		args = append(args, "--startup-status", options.startupStatus)
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
	resolved, err := resolveEverydaySecureStorage(paths, options)
	if err != nil {
		return writeServiceError(stderr, err)
	}
	options = resolved
	if options.vaultMode == platform.VaultModePassphrase {
		_, _ = fmt.Fprintln(stderr, "codex-folio: passphrase storage requires an unlock on every companion restart")
	}
	status, err := platform.Discover(paths, platform.OwnerOptions{})
	if err != nil {
		// An owner publishes its authenticated descriptor immediately after
		// acquiring the lock. A concurrent plain start can observe that narrow
		// interval; reuse it if publication completes, while retaining the
		// original fail-closed metadata error when it does not.
		if apperrors.Code(err) != apperrors.PlatformServiceMetadataInvalid {
			return writeServiceError(stderr, err)
		}
		if connection, waitErr := waitForCompanionClient(paths, time.Second, ""); waitErr == nil {
			return useEverydayCompanion(connection, options, input, stdout, stderr)
		}
		return writeServiceError(stderr, err)
	}
	if !status.Running {
		if start == nil {
			return writeServiceError(stderr, apperrors.New(apperrors.PlatformServiceUnavailable, errors.New("companion starter is unavailable")))
		}
		connection, startedOptions, startErr := startEverydayCompanionWithGuidance(paths, options, input, stdout, stderr, start)
		if startErr != nil {
			return writeServiceError(stderr, startErr)
		}
		return useEverydayCompanion(connection, startedOptions, input, stdout, stderr)
	}

	connection, err := waitForCompanionClient(paths, companionStartupTimeout, "")
	if err != nil {
		return writeServiceError(stderr, err)
	}
	return useEverydayCompanion(connection, options, input, stdout, stderr)
}

func startEverydayCompanionWithGuidance(paths platform.Paths, options serviceOptions, input io.Reader, stdout, stderr io.Writer, start companionProcessStarter) (platform.ServiceClient, serviceOptions, error) {
	for attempts := 0; attempts < 2; attempts++ {
		statusPath, err := prepareCompanionStartupStatus(paths)
		if err != nil {
			return platform.ServiceClient{}, serviceOptions{}, err
		}
		options.startupStatus = statusPath
		if err := start(paths, options); err != nil {
			_ = os.Remove(statusPath)
			return platform.ServiceClient{}, serviceOptions{}, err
		}
		connection, waitErr := waitForCompanionClient(paths, companionStartupTimeout, statusPath)
		_ = os.Remove(statusPath)
		if waitErr == nil {
			options.startupStatus = ""
			return connection, options, nil
		}
		code := apperrors.Code(waitErr)
		if options.vaultMode != "" || runtime.GOOS != "linux" || (code != apperrors.VaultUnavailable && code != apperrors.VaultLocked) {
			writeNativeSecureStorageGuidance(stderr, code)
			return platform.ServiceClient{}, serviceOptions{}, secureStorageGuidanceError(code, waitErr)
		}
		choice, choiceErr := promptSecureStorageRecovery(input, stdout)
		if choiceErr != nil || choice == "q" {
			_, _ = fmt.Fprintln(stderr, "codex-folio: secure-storage setup cancelled; rerun codex-folio to retry")
			return platform.ServiceClient{}, serviceOptions{}, apperrors.New(code, errors.New("secure-storage setup was cancelled; rerun codex-folio to retry"))
		}
		if choice == "2" {
			options.vaultMode = platform.VaultModePassphrase
			_, _ = fmt.Fprintln(stderr, "codex-folio: passphrase storage requires an unlock on every companion restart")
		}
	}
	return platform.ServiceClient{}, serviceOptions{}, apperrors.New(apperrors.VaultUnavailable, errors.New("secure-storage setup did not complete"))
}

func promptSecureStorageRecovery(input io.Reader, output io.Writer) (string, error) {
	_, _ = fmt.Fprintln(output, "Linux Secret Service is unavailable or locked. Unlock your login keyring and retry, or choose passphrase storage.")
	_, _ = fmt.Fprintln(output, "Passphrase storage requires interaction on every companion restart.")
	_, _ = fmt.Fprint(output, "Choose [1] retry Secret Service, [2] use passphrase, or [q] cancel: ")
	line, err := readCompanionSetupLine(input)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	choice := strings.ToLower(strings.TrimSpace(line))
	if choice != "1" && choice != "2" && choice != "q" {
		return "q", nil
	}
	return choice, nil
}

func readCompanionSetupLine(input io.Reader) (string, error) {
	var line strings.Builder
	buffer := make([]byte, 1)
	for {
		count, err := input.Read(buffer)
		if count == 1 {
			if buffer[0] == '\n' {
				return line.String(), nil
			}
			if buffer[0] != '\r' {
				line.WriteByte(buffer[0])
			}
		}
		if err != nil {
			return line.String(), err
		}
	}
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

func waitForCompanionClient(paths platform.Paths, timeout time.Duration, startupStatus string) (platform.ServiceClient, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		connection, err := platform.DiscoverServiceClient(paths, platform.OwnerOptions{})
		if err == nil {
			return connection, nil
		}
		lastErr = err
		if startupStatus != "" {
			if code, statusErr := readCompanionStartupStatus(startupStatus); statusErr == nil && code != "" && code != companionStartupReady {
				return platform.ServiceClient{}, apperrors.New(code, errors.New("detached companion startup failed"))
			}
		}
		if !time.Now().Before(deadline) {
			return platform.ServiceClient{}, apperrors.New(apperrors.HTTPAPIServiceUnavailable, lastErr)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

type secureStorageSelection struct {
	Version int    `json:"version"`
	Mode    string `json:"mode"`
}

func resolveEverydaySecureStorage(paths platform.Paths, options serviceOptions) (serviceOptions, error) {
	if options.vaultMode == "" {
		data, err := os.ReadFile(paths.SecureStorageFile)
		switch {
		case err == nil:
			var selection secureStorageSelection
			if json.Unmarshal(data, &selection) != nil || selection.Version != secureStorageSelectionVersion || (selection.Mode != "native" && selection.Mode != string(platform.VaultModePassphrase)) {
				return serviceOptions{}, apperrors.New(apperrors.VaultUnavailable, errors.New("secure-storage selection is invalid"))
			}
			if selection.Mode == string(platform.VaultModePassphrase) {
				options.vaultMode = platform.VaultModePassphrase
			}
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return serviceOptions{}, apperrors.New(apperrors.VaultUnavailable, err)
		}
	}
	if options.vaultMode != platform.VaultModePassphrase && existingPassphraseInstallation(paths) {
		return serviceOptions{}, apperrors.New(apperrors.VaultMigrationRequired, errors.New("existing passphrase-protected state must be migrated before native storage can start"))
	}
	return options, nil
}

func existingPassphraseInstallation(paths platform.Paths) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	database, err := os.Stat(paths.DatabaseFile)
	if err != nil || !database.Mode().IsRegular() || database.Size() == 0 {
		return false
	}
	file, err := os.Open(paths.VaultFile)
	if err != nil {
		return false
	}
	defer file.Close()
	magic := make([]byte, 4)
	_, err = io.ReadFull(file, magic)
	return err == nil && string(magic) == "CFPV"
}

func rememberEverydaySecureStorage(paths platform.Paths, mode platform.VaultMode) error {
	selected := "native"
	if mode == platform.VaultModePassphrase {
		selected = string(mode)
	}
	encoded, err := json.Marshal(secureStorageSelection{Version: secureStorageSelectionVersion, Mode: selected})
	if err != nil {
		return apperrors.New(apperrors.VaultUnavailable, err)
	}
	if current, readErr := os.ReadFile(paths.SecureStorageFile); readErr == nil && string(current) == string(encoded) {
		return nil
	} else if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return apperrors.New(apperrors.VaultUnavailable, readErr)
	}
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		return apperrors.New(apperrors.VaultUnavailable, err)
	}
	temporary, err := os.CreateTemp(paths.Root, ".secure-storage-*.tmp")
	if err != nil {
		return apperrors.New(apperrors.VaultUnavailable, err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return apperrors.New(apperrors.VaultUnavailable, err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return apperrors.New(apperrors.VaultUnavailable, err)
	}
	if err := temporary.Close(); err != nil {
		return apperrors.New(apperrors.VaultUnavailable, err)
	}
	if err := os.Rename(temporaryName, paths.SecureStorageFile); err != nil {
		return apperrors.New(apperrors.VaultUnavailable, err)
	}
	return nil
}

func prepareCompanionStartupStatus(paths platform.Paths) (string, error) {
	if err := os.MkdirAll(paths.Runtime, 0o700); err != nil {
		return "", apperrors.New(apperrors.PlatformServiceUnavailable, err)
	}
	file, err := os.CreateTemp(paths.Runtime, ".companion-startup-*.status")
	if err != nil {
		return "", apperrors.New(apperrors.PlatformServiceUnavailable, err)
	}
	name := file.Name()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return "", apperrors.New(apperrors.PlatformServiceUnavailable, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", apperrors.New(apperrors.PlatformServiceUnavailable, err)
	}
	return name, nil
}

func validateCompanionStartupStatusPath(paths platform.Paths, path string) error {
	cleanPath := filepath.Clean(path)
	base := filepath.Base(cleanPath)
	if filepath.Dir(cleanPath) != filepath.Clean(paths.Runtime) ||
		!strings.HasPrefix(base, ".companion-startup-") ||
		!strings.HasSuffix(base, ".status") {
		return errors.New("startup status path is outside the runtime directory")
	}
	info, err := os.Lstat(cleanPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("startup status file is unavailable")
	}
	return nil
}

func writeCompanionStartupStatus(path, status string) error {
	if !strings.HasPrefix(status, "CF_") && status != companionStartupReady {
		return errors.New("invalid startup status")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	_, writeErr := io.WriteString(file, status+"\n")
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

func readCompanionStartupStatus(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	status := strings.TrimSpace(string(data))
	if status == "" || status == companionStartupReady || strings.HasPrefix(status, "CF_") {
		return status, nil
	}
	return "", errors.New("invalid startup status")
}

type companionStartupDiagnosticWriter struct {
	io.Writer
	path string
}

func (writer *companionStartupDiagnosticWriter) Write(data []byte) (int, error) {
	if start := strings.Index(string(data), "[CF_"); start >= 0 {
		if end := strings.Index(string(data[start:]), "]"); end > 1 {
			_ = writeCompanionStartupStatus(writer.path, string(data[start+1:start+end]))
		}
	}
	return writer.Writer.Write(data)
}

func secureStorageGuidanceError(code string, cause error) error {
	switch runtime.GOOS {
	case "darwin":
		return apperrors.New(code, errors.Join(cause, errors.New("unlock macOS Keychain and rerun codex-folio")))
	case "windows":
		return apperrors.New(code, errors.Join(cause, errors.New("sign in with the same Windows user and rerun codex-folio")))
	default:
		return cause
	}
}

func writeNativeSecureStorageGuidance(output io.Writer, code string) {
	if code != apperrors.VaultUnavailable && code != apperrors.VaultLocked {
		return
	}
	switch runtime.GOOS {
	case "darwin":
		_, _ = fmt.Fprintln(output, "codex-folio: unlock your login Keychain, allow CodexFolio access, and rerun codex-folio")
	case "windows":
		_, _ = fmt.Fprintln(output, "codex-folio: sign in as the Windows user that owns this state and rerun codex-folio")
	default:
		_, _ = fmt.Fprintln(output, "codex-folio: unlock your Linux login keyring and ensure Secret Service plus secret-tool are available, then rerun codex-folio")
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
