package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"unicode"

	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/continuation"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
)

type handoffOptions struct {
	launchOptions
	historyThreadID string
}

type historyCandidateReader interface {
	Read(context.Context, continuation.HistoryReadRequest) (continuation.CheckpointFields, error)
}

func runHandoff(args []string, stdout, stderr io.Writer, resolvePaths servicePathResolver, resolver launch.ExecutableResolver) int {
	return runHandoffWithDependencies(args, os.Stdin, stdout, stderr, resolvePaths, resolver, openServiceStoreWithVaultMode, newForegroundProcess, newServiceDiagnosticSink(), editCheckpointFile, func() profile.Authenticator {
		return codexadapter.NewAuthenticator()
	}, platform.OwnerOptions{})
}

func runHandoffWithDependencies(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, resolver launch.ExecutableResolver, openStore profileStoreOpener, newProcess launchProcessFactory, diagnosticSink diagnostics.Sink, editor func(string, io.Reader, io.Writer, io.Writer) error, newAuthenticator profileAuthenticatorFactory, ownerOptions platform.OwnerOptions) int {
	return runHandoffWithHistoryDependencies(args, input, stdout, stderr, resolvePaths, resolver, openStore, newProcess, diagnosticSink, editor, newAuthenticator, ownerOptions, codexadapter.NewHistoryReader())
}

func runHandoffWithHistoryDependencies(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, resolver launch.ExecutableResolver, openStore profileStoreOpener, newProcess launchProcessFactory, diagnosticSink diagnostics.Sink, editor func(string, io.Reader, io.Writer, io.Writer) error, newAuthenticator profileAuthenticatorFactory, ownerOptions platform.OwnerOptions, history historyCandidateReader) int {
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
	return withLaunchCommandService(input, stderr, paths, options.launchOptions, openStore, diagnosticSink, newAuthenticator, ownerOptions, func(client *httpapi.CommandClient, childInput io.Reader) int {
		return runHandoffJourneyWithHistory(client, target, capture, report, bufferedReader(childInput), stdout, stderr, newProcess, diagnosticSink, editor, options.historyThreadID, history)
	})
}

func runHandoffJourney(client *httpapi.CommandClient, target string, capture httpapi.CommandCheckpointRequest, report launch.Discovery, input io.Reader, stdout, stderr io.Writer, newProcess launchProcessFactory, diagnosticSink diagnostics.Sink, editor func(string, io.Reader, io.Writer, io.Writer) error) int {
	return runHandoffJourneyWithHistory(client, target, capture, report, bufferedReader(input), stdout, stderr, newProcess, diagnosticSink, editor, "", nil)
}

func runHandoffJourneyWithHistory(client *httpapi.CommandClient, target string, capture httpapi.CommandCheckpointRequest, report launch.Discovery, input *bufio.Reader, stdout, stderr io.Writer, newProcess launchProcessFactory, diagnosticSink diagnostics.Sink, editor func(string, io.Reader, io.Writer, io.Writer) error, historyThreadID string, history historyCandidateReader) int {
	captured, err := client.Checkpoint(context.Background(), capture)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	var approved continuation.Checkpoint
	var ok bool
	if historyThreadID != "" && history != nil && consentHistory(input, stderr) {
		approved, ok, err = reviewAssistedCheckpoint(input, stdout, stderr, client, captured.Checkpoint, capture, report, historyThreadID, history)
		if errors.Is(err, continuation.ErrHistoryUnavailable) {
			io.WriteString(stdout, "Stored history is unavailable; continuing with repository-first review.\n")
			err = nil
		} else if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		} else if !ok {
			return exitSuccess
		}
	}
	if !ok {
		approved, ok, err = reviewCheckpoint(input, stdout, stderr, client, httpapi.CommandCheckpointRequest{Action: "review", ID: captured.Checkpoint.ID}, func(path string) error {
			if editor == nil {
				return errors.New("checkpoint editor is unavailable")
			}
			return editor(path, input, stdout, stderr)
		})
	}
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
	exitStatus, _ := runForegroundLaunch(client, *prepared.Plan, report, input, stdout, stderr, newProcess, diagnosticSink)
	return exitStatus
}

func consentHistory(input *bufio.Reader, stderr io.Writer) bool {
	io.WriteString(stderr, "Type 'assist' to read the named stored source thread for this handoff; anything else uses repository-only: ")
	line, err := input.ReadString('\n')
	return (err == nil || errors.Is(err, io.EOF)) && strings.TrimSpace(line) == "assist"
}

func reviewAssistedCheckpoint(input *bufio.Reader, stdout, stderr io.Writer, client *httpapi.CommandClient, captured continuation.Checkpoint, capture httpapi.CommandCheckpointRequest, report launch.Discovery, threadID string, history historyCandidateReader) (continuation.Checkpoint, bool, error) {
	prepared, err := client.Checkpoint(context.Background(), httpapi.CommandCheckpointRequest{Action: "history-source", ID: captured.ID, Revision: captured.Revision})
	if err != nil || prepared.HistorySource == nil {
		return continuation.Checkpoint{}, false, continuation.ErrHistoryUnavailable
	}
	candidates, err := history.Read(context.Background(), continuation.HistoryReadRequest{
		Executable: report.Executable, IdentityHome: prepared.HistorySource.IdentityHome, ThreadID: threadID,
	})
	if err != nil {
		return continuation.Checkpoint{}, false, continuation.ErrHistoryUnavailable
	}
	fields := mergeHistoryCandidates(captured.Fields, candidates)
	fields, err = editAssistedCheckpointFields(input, stdout, stderr, fields)
	if err != nil {
		return continuation.Checkpoint{}, false, err
	}
	request := httpapi.CommandCheckpointRequest{
		Action: "preview-assisted", ID: captured.ID, Revision: captured.Revision, Fields: &fields,
		RedactPaths: capture.RedactPaths, RedactText: capture.RedactText,
	}
	preview, err := client.Checkpoint(context.Background(), request)
	if err != nil {
		return continuation.Checkpoint{}, false, continuation.ErrHistoryUnavailable
	}
	io.WriteString(stdout, "Sanitized transcript-assisted revision:\n")
	writeCheckpoint(stdout, preview.Checkpoint)
	io.WriteString(stderr, "Type 'approve' to approve this sanitized revision; anything else cancels: ")
	line, readErr := input.ReadString('\n')
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return continuation.Checkpoint{}, false, readErr
	}
	if strings.TrimSpace(line) != "approve" {
		io.WriteString(stdout, "Checkpoint remains repository-first draft.\n")
		return preview.Checkpoint, false, nil
	}
	request.Action = "approve-assisted"
	request.PreviewRevision = preview.Checkpoint.Revision
	approved, err := client.Checkpoint(context.Background(), request)
	if err != nil {
		return continuation.Checkpoint{}, false, err
	}
	io.WriteString(stdout, "Approved transcript-assisted checkpoint:\n")
	writeCheckpoint(stdout, approved.Checkpoint)
	return approved.Checkpoint, true, nil
}

func editAssistedCheckpointFields(input *bufio.Reader, stdout, stderr io.Writer, fields continuation.CheckpointFields) (continuation.CheckpointFields, error) {
	for _, value := range []string{fields.Goal.Value, fields.CompletedWork.Value, fields.PendingWork.Value, fields.Risks.Value, fields.NextAction.Value} {
		if strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return continuation.CheckpointFields{}, continuation.ErrHistoryUnavailable
		}
	}
	io.WriteString(stdout, "Transcript candidates (edited in memory; blank keeps the current value):\n")
	entries := []struct {
		label string
		value *string
	}{
		{"Goal", &fields.Goal.Value},
		{"Completed work", &fields.CompletedWork.Value},
		{"Pending work", &fields.PendingWork.Value},
		{"Risks", &fields.Risks.Value},
		{"Next action", &fields.NextAction.Value},
	}
	for _, entry := range entries {
		io.WriteString(stdout, entry.label+": "+*entry.value+"\n")
		io.WriteString(stderr, entry.label+" replacement: ")
		line, err := input.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return continuation.CheckpointFields{}, err
		}
		if replacement := strings.TrimSpace(line); replacement != "" {
			*entry.value = replacement
		}
	}
	return fields, nil
}

func mergeHistoryCandidates(base, candidates continuation.CheckpointFields) continuation.CheckpointFields {
	pairs := [][2]*continuation.Evidence[string]{
		{&base.Goal, &candidates.Goal}, {&base.CompletedWork, &candidates.CompletedWork},
		{&base.PendingWork, &candidates.PendingWork}, {&base.Risks, &candidates.Risks}, {&base.NextAction, &candidates.NextAction},
	}
	for _, pair := range pairs {
		if strings.TrimSpace(pair[1].Value) != "" {
			*pair[0] = *pair[1]
		}
	}
	return base
}

func parseHandoffArguments(args []string) (string, httpapi.CommandCheckpointRequest, handoffOptions, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return "", httpapi.CommandCheckpointRequest{}, handoffOptions{}, errors.New("handoff requires a target profile")
	}
	var codexBin, historyThreadID string
	codexBinSeen, historySeen := false, false
	captureArgs := []string{"capture"}
	for index := 1; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--codex-bin":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") || codexBinSeen {
				return "", httpapi.CommandCheckpointRequest{}, handoffOptions{}, errors.New("codex-bin is invalid")
			}
			codexBinSeen = true
			index++
			codexBin = strings.TrimSpace(args[index])
		case strings.HasPrefix(arg, "--codex-bin="):
			if codexBinSeen {
				return "", httpapi.CommandCheckpointRequest{}, handoffOptions{}, errors.New("codex-bin is invalid")
			}
			codexBinSeen = true
			codexBin = strings.TrimSpace(strings.TrimPrefix(arg, "--codex-bin="))
		case arg == "--history":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") || historySeen {
				return "", httpapi.CommandCheckpointRequest{}, handoffOptions{}, errors.New("history is invalid")
			}
			historySeen = true
			index++
			historyThreadID = strings.TrimSpace(args[index])
		case strings.HasPrefix(arg, "--history="):
			if historySeen {
				return "", httpapi.CommandCheckpointRequest{}, handoffOptions{}, errors.New("history is invalid")
			}
			historySeen = true
			historyThreadID = strings.TrimSpace(strings.TrimPrefix(arg, "--history="))
		default:
			captureArgs = append(captureArgs, arg)
		}
	}
	if codexBinSeen && codexBin == "" {
		return "", httpapi.CommandCheckpointRequest{}, handoffOptions{}, errors.New("codex-bin is invalid")
	}
	if historySeen && launch.ResumeSessionID([]string{"resume", historyThreadID}) == "" {
		return "", httpapi.CommandCheckpointRequest{}, handoffOptions{}, errors.New("history is invalid")
	}
	capture, checkpointOptions, err := parseCheckpointRequest(captureArgs)
	if err != nil || checkpointOptions.json || checkpointOptions.nonInteractive {
		return "", httpapi.CommandCheckpointRequest{}, handoffOptions{}, errors.New("handoff arguments are invalid")
	}
	return args[0], capture, handoffOptions{launchOptions: launchOptions{serviceOptions: checkpointOptions.serviceOptions, codexBin: codexBin}, historyThreadID: historyThreadID}, nil
}

func writeHandoffUsage(stderr io.Writer, sink diagnostics.Sink) int {
	recordServiceDiagnostic(sink, apperrors.CLIUsage, diagnostics.SeverityWarning)
	io.WriteString(stderr, "codex-folio [CF_CLI_USAGE]: invalid handoff arguments\n")
	io.WriteString(stderr, "Usage: codex-folio handoff TARGET [PATH] [checkpoint capture options] [--history THREAD_ID] [--codex-bin PATH] [--state-root PATH] [--vault-mode MODE]\n")
	return exitUsage
}
