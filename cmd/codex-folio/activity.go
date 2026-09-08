package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
)

func runActivity(args []string, stdout, stderr io.Writer, resolvePaths servicePathResolver) int {
	return runActivityWithDependencies(args, os.Stdin, stdout, stderr, resolvePaths, openServiceStoreWithVaultMode, newServiceDiagnosticSink())
}

func runActivityWithDependencies(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, openStore profileStoreOpener, diagnosticSink diagnostics.Sink) int {
	request, options, err := parseActivityRequest(args)
	if err != nil {
		return writeActivityUsage(stderr, diagnosticSink)
	}
	return withSelectionService(input, stderr, resolvePaths, openStore, diagnosticSink, options, platform.OwnerOptions{}, false, func(client *httpapi.CommandClient) error {
		result, err := client.Activity(context.Background(), request)
		if err != nil {
			return err
		}
		if options.json {
			if err := writeActivityJSON(stdout, result.Records); err != nil {
				return apperrors.New(apperrors.CLIInternal, err)
			}
			return nil
		}
		writeActivityTimeline(stdout, result.Records)
		return nil
	})
}

func writeActivityJSON(output io.Writer, records []activity.TimelineRecord) error {
	return writeServiceJSON(output, httpapi.ActivityResponseFor(records))
}

func parseActivityRequest(args []string) (httpapi.CommandActivityRequest, selectionOptions, error) {
	if len(args) == 0 {
		return httpapi.CommandActivityRequest{}, selectionOptions{}, errors.New("activity action is required")
	}
	request := httpapi.CommandActivityRequest{Action: args[0]}
	operands, common := []string{}, []string{}
	for index := 1; index < len(args); index++ {
		switch arg := args[index]; {
		case arg == "--profile" || arg == "--project":
			if index+1 >= len(args) {
				return httpapi.CommandActivityRequest{}, selectionOptions{}, errors.New("activity filter requires a value")
			}
			index++
			if arg == "--profile" && request.ProfileAlias == "" {
				request.ProfileAlias = args[index]
			} else if arg == "--project" && request.ProjectID == "" {
				request.ProjectID = args[index]
			} else {
				return httpapi.CommandActivityRequest{}, selectionOptions{}, errors.New("activity filter may be supplied once")
			}
		case strings.HasPrefix(arg, "--profile=") && request.ProfileAlias == "":
			request.ProfileAlias = strings.TrimPrefix(arg, "--profile=")
		case strings.HasPrefix(arg, "--project=") && request.ProjectID == "":
			request.ProjectID = strings.TrimPrefix(arg, "--project=")
		case arg == "--state-root" || arg == "--vault-mode":
			if index+1 >= len(args) {
				return httpapi.CommandActivityRequest{}, selectionOptions{}, errors.New("common option requires a value")
			}
			common = append(common, arg, args[index+1])
			index++
		case arg == "--json" || strings.HasPrefix(arg, "--state-root=") || strings.HasPrefix(arg, "--vault-mode="):
			common = append(common, arg)
		case strings.HasPrefix(arg, "--"):
			return httpapi.CommandActivityRequest{}, selectionOptions{}, errors.New("unknown activity option")
		default:
			operands = append(operands, arg)
		}
	}
	options, err := parseSelectionOptions(common)
	if err != nil {
		return httpapi.CommandActivityRequest{}, selectionOptions{}, err
	}
	if (request.ProfileAlias != "" && strings.TrimSpace(request.ProfileAlias) == "") || (request.ProjectID != "" && strings.TrimSpace(request.ProjectID) == "") {
		return httpapi.CommandActivityRequest{}, selectionOptions{}, errors.New("activity filter requires a value")
	}
	switch request.Action {
	case "refresh":
		if len(operands) != 1 || request.ProfileAlias != "" || request.ProjectID != "" {
			return httpapi.CommandActivityRequest{}, selectionOptions{}, errors.New("refresh requires one profile")
		}
		request.Alias = operands[0]
	case "list":
		if len(operands) != 0 {
			return httpapi.CommandActivityRequest{}, selectionOptions{}, errors.New("list accepts filters only")
		}
	default:
		return httpapi.CommandActivityRequest{}, selectionOptions{}, errors.New("unknown activity action")
	}
	return request, options, nil
}

func writeActivityTimeline(output io.Writer, records []activity.TimelineRecord) {
	for _, record := range records {
		project := record.ProjectAlias
		if project == "" {
			project = record.ProjectBasename
		}
		if project == "" {
			project = "unassociated"
		}
		source := record.Source
		if record.SourceVersion != "" {
			source += " " + record.SourceVersion
		}
		fmt.Fprintf(output, "%s: %s | %s | %s | %s to %s | %s | %s | correlation %s",
			activityRecordLabel(record.RecordType), record.ID, record.ProfileAlias, project,
			record.StartedAt.UTC().Format("2006-01-02T15:04:05Z07:00"), record.LastObservedAt.UTC().Format("2006-01-02T15:04:05Z07:00"), source, record.Provenance, record.Correlation.State)
		if record.Correlation.EvidenceType != "" {
			fmt.Fprintf(output, " (%s, %s)", record.Correlation.EvidenceType, record.Correlation.Confidence)
		}
		if record.Lifecycle != "" {
			fmt.Fprintf(output, " | %s", record.Lifecycle)
			if record.ExitStatus != nil {
				fmt.Fprintf(output, " exit %d", *record.ExitStatus)
			}
		}
		if record.Model != "" {
			fmt.Fprintf(output, " | model %s", record.Model)
		}
		if record.TokensUsed != nil {
			fmt.Fprintf(output, " | tokens %s", strconv.FormatInt(*record.TokensUsed, 10))
		}
		fmt.Fprintln(output)
	}
}

func activityRecordLabel(recordType string) string {
	if recordType == activity.RecordTypeManagedLaunch {
		return activity.ProvenanceManagedLaunch
	}
	return "Observed Session"
}

func writeActivityUsage(stderr io.Writer, diagnosticSink diagnostics.Sink) int {
	recordServiceDiagnostic(diagnosticSink, apperrors.CLIUsage, diagnostics.SeverityWarning)
	fmt.Fprintf(stderr, "codex-folio [%s]: invalid activity arguments\n", apperrors.CLIUsage)
	io.WriteString(stderr, "Usage: codex-folio activity {refresh ALIAS|list [--profile ALIAS] [--project ID]} [--state-root PATH] [--vault-mode MODE] [--json]\n")
	return exitUsage
}
