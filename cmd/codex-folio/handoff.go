package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
)

func runHandoff(args []string, stdout, stderr io.Writer, resolvePaths servicePathResolver, resolver launch.ExecutableResolver) int {
	return runHandoffWithDependencies(args, os.Stdin, stdout, stderr, resolvePaths, resolver, openServiceStoreWithVaultMode, newForegroundProcess, newServiceDiagnosticSink(), editCheckpointFile, func() profile.Authenticator {
		return codexadapter.NewAuthenticator()
	}, platform.OwnerOptions{})
}

func runHandoffWithDependencies(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, resolver launch.ExecutableResolver, openStore profileStoreOpener, newProcess launchProcessFactory, diagnosticSink diagnostics.Sink, editor func(string, io.Reader, io.Writer, io.Writer) error, newAuthenticator profileAuthenticatorFactory, ownerOptions platform.OwnerOptions) int {
	target, capture, options, err := parseHandoffArguments(args)
	if err != nil || profile.ValidateAlias(target) != nil {
		return writeHandoffUsage(stderr, diagnosticSink)
	}
	paths, err := resolvePaths(options.stateRoot)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	report, err := launch.Discover(resolver, options.codexBin)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	return withLaunchCommandService(input, stderr, paths, options, openStore, diagnosticSink, newAuthenticator, ownerOptions, func(client *httpapi.CommandClient, childInput io.Reader) int {
		captured, err := client.Checkpoint(context.Background(), capture)
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
		approved, ok, err := reviewCheckpoint(childInput, stdout, stderr, client, httpapi.CommandCheckpointRequest{Action: "review", ID: captured.Checkpoint.ID}, func(path string) error {
			if editor == nil {
				return errors.New("checkpoint editor is unavailable")
			}
			return editor(path, childInput, stdout, stderr)
		})
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
		if !ok {
			return exitSuccess
		}
		prepared, err := client.Launch(context.Background(), httpapi.CommandLaunchRequest{
			Action: "prepare-handoff", Alias: target, Executable: report.Executable, Version: report.Version,
			CheckpointID: approved.ID, Revision: approved.Revision,
		})
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
		if prepared.Plan == nil {
			return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.LaunchPlanInvalid, launch.ErrPlanInvalid), diagnosticSink)
		}
		return runForegroundLaunch(client, *prepared.Plan, report, childInput, stdout, stderr, newProcess, diagnosticSink)
	})
}

func parseHandoffArguments(args []string) (string, httpapi.CommandCheckpointRequest, launchOptions, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return "", httpapi.CommandCheckpointRequest{}, launchOptions{}, errors.New("handoff requires a target profile")
	}
	var codexBin string
	codexBinSeen := false
	captureArgs := []string{"capture"}
	for index := 1; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--codex-bin":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") || codexBinSeen {
				return "", httpapi.CommandCheckpointRequest{}, launchOptions{}, errors.New("codex-bin is invalid")
			}
			codexBinSeen = true
			index++
			codexBin = strings.TrimSpace(args[index])
		case strings.HasPrefix(arg, "--codex-bin="):
			if codexBinSeen {
				return "", httpapi.CommandCheckpointRequest{}, launchOptions{}, errors.New("codex-bin is invalid")
			}
			codexBinSeen = true
			codexBin = strings.TrimSpace(strings.TrimPrefix(arg, "--codex-bin="))
		default:
			captureArgs = append(captureArgs, arg)
		}
	}
	if codexBinSeen && codexBin == "" {
		return "", httpapi.CommandCheckpointRequest{}, launchOptions{}, errors.New("codex-bin is invalid")
	}
	capture, checkpointOptions, err := parseCheckpointRequest(captureArgs)
	if err != nil || checkpointOptions.json || checkpointOptions.nonInteractive {
		return "", httpapi.CommandCheckpointRequest{}, launchOptions{}, errors.New("handoff arguments are invalid")
	}
	return args[0], capture, launchOptions{serviceOptions: checkpointOptions.serviceOptions, codexBin: codexBin}, nil
}

func writeHandoffUsage(stderr io.Writer, sink diagnostics.Sink) int {
	recordServiceDiagnostic(sink, apperrors.CLIUsage, diagnostics.SeverityWarning)
	io.WriteString(stderr, "codex-folio [CF_CLI_USAGE]: invalid handoff arguments\n")
	io.WriteString(stderr, "Usage: codex-folio handoff TARGET [PATH] [checkpoint capture options] [--codex-bin PATH] [--state-root PATH] [--vault-mode MODE]\n")
	return exitUsage
}
