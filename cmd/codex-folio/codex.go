package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/launch"
)

type codexOptions struct {
	json     bool
	codexBin string
}

func runCodex(args []string, stdout, stderr io.Writer, resolver launch.ExecutableResolver) int {
	if len(args) == 0 {
		return writeCodexUsage(stderr, "a Codex command is required")
	}
	if args[0] != "discover" {
		return writeCodexUsage(stderr, "unknown Codex command")
	}
	options, err := parseCodexOptions(args[1:])
	if err != nil {
		return writeCodexUsage(stderr, "invalid Codex arguments")
	}

	report, err := launch.Discover(resolver, options.codexBin)
	if err != nil {
		return writeCodexError(stderr, err)
	}
	if options.json {
		if err := writeServiceJSON(stdout, report); err != nil {
			fmt.Fprintf(stderr, "codex-folio [%s]: could not encode Codex discovery\n", apperrors.CLIInternal)
			return exitFailure
		}
		return exitSuccess
	}
	_, _ = fmt.Fprintf(stdout, "Codex executable: %s\n", report.Executable)
	_, _ = fmt.Fprintf(stdout, "Codex version: %s\n", report.Version)
	_, _ = io.WriteString(stdout, "Capability status:\n")
	_, _ = fmt.Fprintf(stdout, "  transparent launch: %s\n", report.Capabilities.TransparentLaunch)
	_, _ = fmt.Fprintf(stdout, "  metadata: %s\n", report.Capabilities.Metadata)
	_, _ = fmt.Fprintf(stdout, "  experimental: %s\n", report.Capabilities.Experimental)
	return exitSuccess
}

func parseCodexOptions(args []string) (codexOptions, error) {
	var options codexOptions
	for index := 0; index < len(args); index++ {
		switch arg := args[index]; {
		case arg == "--json":
			if options.json {
				return codexOptions{}, errors.New("--json may be supplied only once")
			}
			options.json = true
		case arg == "--codex-bin":
			if options.codexBin != "" || index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return codexOptions{}, errors.New("--codex-bin requires one value")
			}
			index++
			options.codexBin = strings.TrimSpace(args[index])
			if options.codexBin == "" {
				return codexOptions{}, errors.New("--codex-bin requires one value")
			}
		case strings.HasPrefix(arg, "--codex-bin="):
			if options.codexBin != "" {
				return codexOptions{}, errors.New("--codex-bin may be supplied only once")
			}
			options.codexBin = strings.TrimSpace(strings.TrimPrefix(arg, "--codex-bin="))
			if options.codexBin == "" {
				return codexOptions{}, errors.New("--codex-bin requires one value")
			}
		default:
			return codexOptions{}, errors.New("unexpected Codex argument")
		}
	}
	return options, nil
}

func writeCodexUsage(stderr io.Writer, message string) int {
	fmt.Fprintf(stderr, "codex-folio [%s]: %s\n", apperrors.CLIUsage, message)
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  codex-folio codex discover [--codex-bin PATH] [--json]")
	return exitUsage
}

func writeCodexError(stderr io.Writer, err error) int {
	code := diagnostics.CodeFor(err, apperrors.CLIInternal)
	fmt.Fprintf(stderr, "codex-folio [%s]: %s\n", code, codexRemediation(code))
	return exitFailure
}

func codexRemediation(code string) string {
	switch code {
	case apperrors.LaunchCodexNotFound:
		return "no usable Codex executable was found on PATH"
	case apperrors.LaunchCodexAmbiguous:
		return "multiple Codex executables were found on PATH; provide one absolute path with --codex-bin"
	case apperrors.LaunchCodexPathInvalid:
		return "the Codex executable path is invalid or not executable"
	case apperrors.LaunchCodexVersionInvalid:
		return "the Codex executable did not return a valid version"
	default:
		return "the Codex executable could not be validated"
	}
}
