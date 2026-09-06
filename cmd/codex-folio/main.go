package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/buildinfo"
	"venkatasudha.com/codex-folio/internal/launch"
)

const (
	exitSuccess = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, buildinfo.Current()))
}

func run(args []string, stdout, stderr io.Writer, metadata buildinfo.Metadata) int {
	return runWithServicePathResolverAndCodexResolver(args, stdout, stderr, metadata, resolveCLIPaths, codexadapter.NewResolver(codexadapter.ResolverOptions{}))
}

func runWithServicePathResolver(args []string, stdout, stderr io.Writer, metadata buildinfo.Metadata, resolvePaths servicePathResolver) int {
	return runWithServicePathResolverAndCodexResolver(args, stdout, stderr, metadata, resolvePaths, codexadapter.NewResolver(codexadapter.ResolverOptions{}))
}

func runWithServicePathResolverAndCodexResolver(args []string, stdout, stderr io.Writer, metadata buildinfo.Metadata, resolvePaths servicePathResolver, resolver launch.ExecutableResolver) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "--state-root") || strings.HasPrefix(args[0], "--vault-mode") {
		options, err := parseSelectionOptions(args)
		if err != nil || options.json {
			return writeSelectionUsageDiagnostic(stderr, "invalid interactive selection arguments", newServiceDiagnosticSink())
		}
		return runInteractiveSelectionWithOptions(os.Stdin, stdout, stderr, resolvePaths, openServiceStoreWithVaultMode, func(alias string) int {
			launchArgs := []string{alias}
			if options.stateRoot != nil {
				launchArgs = append(launchArgs, "--state-root", *options.stateRoot)
			}
			if options.vaultMode != "" {
				launchArgs = append(launchArgs, "--vault-mode", string(options.vaultMode))
			}
			return runLaunch(append(launchArgs, "--"), stdout, stderr, resolvePaths, resolver)
		}, newServiceDiagnosticSink(), options)
	}

	command := args[0]
	switch command {
	case "version", "--version", "-v":
		return runVersion(args, stdout, stderr, metadata)
	case "service":
		return runServiceWithPathResolver(args[1:], stdout, stderr, resolvePaths)
	case "codex":
		return runCodex(args[1:], stdout, stderr, resolver)
	case "profile":
		return runProfile(args[1:], stdout, stderr, resolvePaths, resolver)
	case "launch":
		return runLaunch(args[1:], stdout, stderr, resolvePaths, resolver)
	case "select":
		return runSelect(args[1:], os.Stdin, stdout, stderr, resolvePaths)
	case "configuration-pack":
		return runConfigurationPack(args[1:], os.Stdin, stdout, stderr, resolvePaths)
	case "shell":
		return runShell(args[1:], stdout, stderr, resolvePaths, metadata.Version)
	case "help", "--help", "-h":
		writeUsage(stdout, metadata)
		return exitSuccess
	default:
		fmt.Fprintf(stderr, "codex-folio [%s]: unknown command\n", apperrors.CLIUsage)
		fmt.Fprintln(stderr, "Run 'codex-folio --help' for usage.")
		return exitUsage
	}
}

func runVersion(args []string, stdout, stderr io.Writer, metadata buildinfo.Metadata) int {
	jsonOutput := false
	for _, arg := range args[1:] {
		if arg == "--json" && !jsonOutput {
			jsonOutput = true
			continue
		}
		fmt.Fprintf(stderr, "codex-folio [%s]: unexpected version argument\n", apperrors.CLIUsage)
		return exitUsage
	}

	if jsonOutput {
		encoded, err := metadata.JSON()
		if err != nil {
			fmt.Fprintf(stderr, "codex-folio [%s]: could not encode version metadata\n", apperrors.CLIInternal)
			return exitFailure
		}
		_, _ = fmt.Fprintln(stdout, string(encoded))
		return exitSuccess
	}

	_, _ = io.WriteString(stdout, metadata.Human())
	return exitSuccess
}

func writeUsage(stdout io.Writer, metadata buildinfo.Metadata) {
	fmt.Fprintf(stdout, "%s %s\n", metadata.Product, metadata.Version)
	fmt.Fprintln(stdout, "Local service foundation: encrypted state recovery is available.")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Usage:")
	fmt.Fprintln(stdout, "  codex-folio version [--json]")
	fmt.Fprintln(stdout, "  codex-folio service {status|start|recovery} [--state-root PATH] [--vault-mode secret-service|passphrase] [--json]")
	fmt.Fprintln(stdout, "  codex-folio codex discover [--codex-bin PATH] [--json]")
	fmt.Fprintln(stdout, "  codex-folio profile add ALIAS [--identity-home PATH] [--browser|--device-code] [--codex-bin PATH] [--state-root PATH] [--json]")
	fmt.Fprintln(stdout, "  codex-folio profile reauthenticate ALIAS [--browser|--device-code] [--codex-bin PATH] [--state-root PATH] [--vault-mode MODE] [--non-interactive] [--json]")
	fmt.Fprintln(stdout, "  codex-folio profile list [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(stdout, "  codex-folio profile edit ALIAS [--alias ALIAS] [--display-name NAME] [--email LABEL] [--workspace LABEL] [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(stdout, "  codex-folio profile remove ALIAS [--replacement ALIAS] [--confirm ALIAS] [--state-root PATH] [--vault-mode MODE] [--non-interactive] [--json]")
	fmt.Fprintln(stdout, "  codex-folio profile restore ALIAS [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(stdout, "  codex-folio profile purge ALIAS [--confirm ALIAS] [--state-root PATH] [--vault-mode MODE] [--non-interactive] [--json]")
	fmt.Fprintln(stdout, "  codex-folio launch ALIAS [--codex-bin PATH] [--state-root PATH] [--vault-mode MODE] -- [CODEX ARGS ...]")
	fmt.Fprintln(stdout, "  codex-folio select ALIAS [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(stdout, "  codex-folio configuration-pack {create|approve|assign|override|preview|project|promotion-preview|promote} ...")
	fmt.Fprintln(stdout, "  codex-folio shell {generate|remove} [--shell bash|zsh|powershell] [--wrapper] [--state-root PATH] [--json]")
	fmt.Fprintln(stdout, "  codex-folio --help")
}
