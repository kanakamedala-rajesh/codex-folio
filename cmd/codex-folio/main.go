package main

import (
	"fmt"
	"io"
	"os"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/buildinfo"
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
	return runWithServicePathResolver(args, stdout, stderr, metadata, resolveCLIPaths)
}

func runWithServicePathResolver(args []string, stdout, stderr io.Writer, metadata buildinfo.Metadata, resolvePaths servicePathResolver) int {
	if len(args) == 0 {
		writeUsage(stdout, metadata)
		return exitSuccess
	}

	command := args[0]
	switch command {
	case "version", "--version", "-v":
		return runVersion(args, stdout, stderr, metadata)
	case "service":
		return runServiceWithPathResolver(args[1:], stdout, stderr, resolvePaths)
	case "help", "--help", "-h":
		writeUsage(stdout, metadata)
		return exitSuccess
	default:
		fmt.Fprintf(stderr, "codex-folio [%s]: unknown command %q\n", apperrors.CLIUsage, command)
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
		fmt.Fprintf(stderr, "codex-folio [%s]: unexpected version argument %q\n", apperrors.CLIUsage, arg)
		return exitUsage
	}

	if jsonOutput {
		encoded, err := metadata.JSON()
		if err != nil {
			fmt.Fprintf(stderr, "codex-folio [%s]: could not encode version metadata: %v\n", apperrors.CLIInternal, err)
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
	fmt.Fprintln(stdout, "  codex-folio --help")
}
