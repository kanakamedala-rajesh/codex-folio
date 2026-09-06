package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
)

func runUsage(args []string, stdout, stderr io.Writer, resolvePaths servicePathResolver) int {
	diagnosticSink := newServiceDiagnosticSink()
	if len(args) < 2 || args[0] != "refresh" {
		return writeSelectionUsageDiagnostic(stderr, "usage requires: refresh ALIAS", diagnosticSink)
	}
	alias := args[1]
	options, err := parseSelectionOptions(args[2:])
	if err != nil {
		return writeSelectionUsageDiagnostic(stderr, "invalid usage arguments", diagnosticSink)
	}
	return withSelectionService(os.Stdin, stderr, resolvePaths, openServiceStoreWithVaultMode, diagnosticSink, options, platform.OwnerOptions{}, false, func(client *httpapi.CommandClient) error {
		result, err := client.RefreshUsage(context.Background(), alias)
		if err != nil {
			return err
		}
		if options.json {
			if err := writeServiceJSON(stdout, result); err != nil {
				return apperrors.New(apperrors.CLIInternal, err)
			}
			return nil
		}
		writeUsageSnapshot(stdout, result)
		return nil
	})
}

func writeUsageSnapshot(output io.Writer, snapshot httpapi.UsageSnapshotResponse) {
	fmt.Fprintf(output, "Usage Snapshot: %s\n", snapshot.Alias)
	fmt.Fprintf(output, "Captured: %s from %s %s\n", snapshot.CapturedAt, snapshot.Source, snapshot.SourceVersion)
	observations := make(map[string]httpapi.UsageObservation, len(snapshot.Observations))
	for _, observation := range snapshot.Observations {
		observations[observation.MetricKey] = observation
	}
	for _, availability := range snapshot.Availability {
		if observation, ok := observations[availability.MetricKey]; ok {
			fmt.Fprintf(output, "%s: %g %s (%s, %s", observation.MetricKey, observation.Value, observation.Unit, observation.Provenance, observation.Freshness)
			if observation.WindowStart != "" || observation.WindowEnd != "" {
				fmt.Fprintf(output, ", window %s to %s", observation.WindowStart, observation.WindowEnd)
			}
			fmt.Fprintln(output, ")")
			continue
		}
		fmt.Fprintf(output, "%s: %s\n", availability.MetricKey, availability.State)
	}
}
