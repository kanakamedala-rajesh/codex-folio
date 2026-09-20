package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
)

type vaultCommandOptions struct {
	stateRoot *string
	json      bool
}

func runVault(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver) int {
	if len(args) == 0 || args[0] != "unlock" {
		return writeVaultUsage(stderr)
	}
	options, err := parseVaultCommandOptions(args[1:])
	if err != nil {
		return writeVaultUsage(stderr)
	}
	paths, err := resolvePaths(options.stateRoot)
	if err != nil {
		return writeServiceError(stderr, err)
	}
	status, err := platform.Discover(paths, platform.OwnerOptions{})
	if err != nil {
		return writeServiceError(stderr, err)
	}
	if !status.Running {
		return writeServiceError(stderr, apperrors.New(apperrors.HTTPAPIServiceUnavailable, errors.New("service owner is not running")))
	}
	connection, err := platform.DiscoverServiceClient(paths, platform.OwnerOptions{})
	if err != nil {
		return writeServiceError(stderr, err)
	}
	passphrase, err := readVaultUnlockPassphrase(input, stderr)
	if err != nil {
		return writeServiceError(stderr, err)
	}
	health, err := httpapi.NewCommandClient(connection.Origin, connection.Token, nil).UnlockVault(context.Background(), passphrase)
	passphrase = ""
	if err != nil {
		return writeServiceError(stderr, err)
	}
	if options.json {
		if err := writeServiceJSON(stdout, health); err != nil {
			return writeServiceError(stderr, apperrors.New(apperrors.CLIInternal, err))
		}
		return exitSuccess
	}
	_, _ = io.WriteString(stdout, "vault unlocked for this service session; state-owning workflows are active\n")
	return exitSuccess
}

func readVaultUnlockPassphrase(input io.Reader, stderr io.Writer) (string, error) {
	if input == nil {
		return "", apperrors.New(apperrors.VaultLocked, errors.New("vault unlock requires private passphrase input"))
	}
	if file, ok := input.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		_, _ = io.WriteString(stderr, "Vault passphrase: ")
		passphrase, err := term.ReadPassword(int(file.Fd()))
		_, _ = io.WriteString(stderr, "\n")
		if err != nil || len(passphrase) == 0 {
			clear(passphrase)
			return "", apperrors.New(apperrors.VaultLocked, errors.New("vault passphrase input is unavailable"))
		}
		result := string(passphrase)
		clear(passphrase)
		return result, nil
	}
	reader, ok := input.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReader(input)
	}
	return readServiceVaultPassphrase(reader)
}

func parseVaultCommandOptions(args []string) (vaultCommandOptions, error) {
	var options vaultCommandOptions
	for index := 0; index < len(args); index++ {
		switch arg := args[index]; {
		case arg == "--json":
			if options.json {
				return vaultCommandOptions{}, errors.New("--json may be supplied only once")
			}
			options.json = true
		case arg == "--state-root":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return vaultCommandOptions{}, errors.New("--state-root requires a value")
			}
			index++
			if err := setVaultStateRoot(&options, args[index]); err != nil {
				return vaultCommandOptions{}, err
			}
		case strings.HasPrefix(arg, "--state-root="):
			if err := setVaultStateRoot(&options, strings.TrimPrefix(arg, "--state-root=")); err != nil {
				return vaultCommandOptions{}, err
			}
		default:
			return vaultCommandOptions{}, errors.New("unexpected vault unlock argument")
		}
	}
	return options, nil
}

func setVaultStateRoot(options *vaultCommandOptions, value string) error {
	if options.stateRoot != nil || strings.TrimSpace(value) == "" {
		return errors.New("state-root must be supplied exactly once with a value")
	}
	options.stateRoot = &value
	return nil
}

func writeVaultUsage(stderr io.Writer) int {
	_, _ = fmt.Fprintf(stderr, "codex-folio [%s]: invalid vault command\n", apperrors.CLIUsage)
	_, _ = io.WriteString(stderr, "Usage: codex-folio vault unlock [--state-root PATH] [--json]\n")
	return exitUsage
}
