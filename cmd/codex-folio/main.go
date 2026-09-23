package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/buildinfo"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
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
	return runWithServicePathResolverAndCodexResolverAndForegroundDependencies(
		args, os.Stdin, stdout, stderr, metadata, resolvePaths, resolver,
		openServiceStoreWithVaultMode, newForegroundProcess,
		func() profile.Authenticator { return codexadapter.NewAuthenticator() },
	)
}

func runWithServicePathResolverAndCodexResolverAndForegroundDependencies(args []string, input io.Reader, stdout, stderr io.Writer, metadata buildinfo.Metadata, resolvePaths servicePathResolver, resolver launch.ExecutableResolver, openStore profileStoreOpener, newProcess launchProcessFactory, newAuthenticator profileAuthenticatorFactory) int {
	return runWithServicePathResolverAndCodexResolverAndForegroundDependenciesAndCompanionStarter(
		args, input, stdout, stderr, metadata, resolvePaths, resolver, openStore, newProcess, newAuthenticator,
		startDetachedCompanion,
	)
}

func runWithServicePathResolverAndCodexResolverAndForegroundDependenciesAndCompanionStarter(args []string, input io.Reader, stdout, stderr io.Writer, metadata buildinfo.Metadata, resolvePaths servicePathResolver, resolver launch.ExecutableResolver, openStore profileStoreOpener, newProcess launchProcessFactory, newAuthenticator profileAuthenticatorFactory, startCompanion companionProcessStarter) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "--state-root") || strings.HasPrefix(args[0], "--vault-mode") {
		options, err := parseSelectionOptions(args)
		if err != nil || options.json {
			return writeSelectionUsageDiagnostic(stderr, "invalid interactive selection arguments", newServiceDiagnosticSink())
		}
		promptInput, childInput := foregroundInputs(input)
		paths, err := resolvePaths(options.stateRoot)
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, newServiceDiagnosticSink())
		}
		if code := ensureEverydayCompanion(paths, options.serviceOptions, promptInput, stdout, stderr, startCompanion); code != exitSuccess {
			return code
		}
		if proceed, code := guideFirstProfile(paths, promptInput, stdout, stderr); !proceed {
			return code
		}
		var authenticator profile.Authenticator
		if newAuthenticator != nil {
			authenticator = newAuthenticator()
		}
		return runInteractiveSelectionWithOptionsAndAuthenticator(promptInput, stdout, stderr, resolvePaths, openStore, func(alias string) int {
			launchArgs := []string{alias}
			if options.stateRoot != nil {
				launchArgs = append(launchArgs, "--state-root", *options.stateRoot)
			}
			if options.vaultMode != "" {
				launchArgs = append(launchArgs, "--vault-mode", string(options.vaultMode))
			}
			return runLaunchWithInputAndDependenciesAndOwnerOptionsAndAuthenticator(
				append(launchArgs, "--"), childInput, stdout, stderr, resolvePaths, resolver,
				openStore, newProcess, newServiceDiagnosticSink(), newAuthenticator, platform.OwnerOptions{},
			)
		}, newServiceDiagnosticSink(), options, authenticator)
	}

	command := args[0]
	switch command {
	case "version", "--version", "-v":
		return runVersion(args, stdout, stderr, metadata)
	case "service":
		return runServiceWithPathResolver(args[1:], stdout, stderr, resolvePaths)
	case "vault":
		return runVault(args[1:], os.Stdin, stdout, stderr, resolvePaths)
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
	case "configuration":
		return runConfiguration(args[1:], stdout, stderr, resolvePaths)
	case "usage":
		return runUsage(args[1:], stdout, stderr, resolvePaths)
	case "project":
		return runProject(args[1:], stdout, stderr, resolvePaths)
	case "activity":
		return runActivity(args[1:], stdout, stderr, resolvePaths)
	case "analytics":
		return runAnalyticsWithDependencies(args[1:], os.Stdin, stdout, stderr, resolvePaths, openServiceStoreWithVaultMode, newServiceDiagnosticSink())
	case "settings":
		return runSettings(args[1:], stdout, stderr, resolvePaths)
	case "diagnostics":
		return runDiagnostics(args[1:], os.Stdin, stdout, stderr, resolvePaths)
	case "updates":
		return runUpdates(args[1:], stdout, stderr, resolvePaths)
	case "telemetry":
		return runTelemetry(args[1:], stdout, stderr, resolvePaths)
	case "checkpoint":
		return runCheckpoint(args[1:], stdout, stderr, resolvePaths)
	case "handoff":
		return runHandoff(args[1:], stdout, stderr, resolvePaths, resolver)
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

func foregroundInputs(input io.Reader) (io.Reader, io.Reader) {
	if file, ok := input.(*os.File); ok {
		return &foregroundPromptInput{
			Reader:   bufio.NewReader(singleByteReader{reader: file}),
			terminal: file,
		}, file
	}
	reader := bufferedReader(input)
	return reader, reader
}

type foregroundPromptInput struct {
	*bufio.Reader
	terminal *os.File
}

type singleByteReader struct{ reader io.Reader }

func (reader singleByteReader) Read(buffer []byte) (int, error) {
	if len(buffer) > 1 {
		buffer = buffer[:1]
	}
	return reader.reader.Read(buffer)
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
	fmt.Fprintln(stdout, "  codex-folio service {certificate|install|start|status|uninstall|recovery} [--state-root PATH] [--vault-mode secret-service|wsl-dpapi|passphrase] [--json]")
	fmt.Fprintln(stdout, "  codex-folio vault unlock [--state-root PATH] [--json]")
	fmt.Fprintln(stdout, "  codex-folio codex discover [--codex-bin PATH] [--json]")
	fmt.Fprintln(stdout, "  codex-folio profile add ALIAS [--identity-home PATH] [--browser|--device-code] [--codex-bin PATH] [--state-root PATH] [--json]")
	fmt.Fprintln(stdout, "  codex-folio profile reauthenticate ALIAS [--browser|--device-code] [--codex-bin PATH] [--state-root PATH] [--vault-mode MODE] [--non-interactive] [--json]")
	fmt.Fprintln(stdout, "  codex-folio profile list [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(stdout, "  codex-folio profile edit ALIAS [--alias ALIAS] [--display-name NAME] [--email LABEL] [--workspace LABEL] [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(stdout, "  codex-folio profile remove ALIAS [--replacement ALIAS] [--confirm ALIAS] [--state-root PATH] [--vault-mode MODE] [--non-interactive] [--json]")
	fmt.Fprintln(stdout, "  codex-folio profile restore ALIAS [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(stdout, "  codex-folio profile purge ALIAS [--confirm ALIAS] [--state-root PATH] [--vault-mode MODE] [--non-interactive] [--json]")
	fmt.Fprintln(stdout, "  codex-folio launch ALIAS [--project ID] [--codex-bin PATH] [--state-root PATH] [--vault-mode MODE] -- [CODEX ARGS ...]")
	fmt.Fprintln(stdout, "  codex-folio select ALIAS [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(stdout, "  codex-folio configuration-pack {create|approve|assign|override|preview|project|promotion-preview|promote} ...")
	fmt.Fprintln(stdout, "  codex-folio configuration {export|import} ...")
	fmt.Fprintln(stdout, "  codex-folio usage refresh ALIAS [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(stdout, "  codex-folio usage show [--combined] [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(stdout, "  codex-folio project {resolve|list|edit|reconcile} ...")
	fmt.Fprintln(stdout, "  codex-folio activity {refresh ALIAS|list [--profile ALIAS] [--project ID]} ...")
	fmt.Fprintln(stdout, "  codex-folio analytics {retention|purge|aggregates} ...")
	fmt.Fprintln(stdout, "  codex-folio settings collection [--active-minutes N] [--idle-minutes N] [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(stdout, "  codex-folio diagnostics {settings|export} ...")
	fmt.Fprintln(stdout, "  codex-folio updates {status|check|settings} ...")
	fmt.Fprintln(stdout, "  codex-folio telemetry {status|schema|enable|revoke|reset-id} ...")
	fmt.Fprintln(stdout, "  codex-folio checkpoint {retention|capture|show|review|export} ...")
	fmt.Fprintln(stdout, "  codex-folio handoff TARGET [PATH] [checkpoint capture options] [--codex-bin PATH] [--state-root PATH] [--vault-mode MODE]")
	fmt.Fprintln(stdout, "  codex-folio shell {generate|remove} [--shell bash|zsh|powershell] [--wrapper] [--state-root PATH] [--json]")
	fmt.Fprintln(stdout, "  codex-folio --help")
}
