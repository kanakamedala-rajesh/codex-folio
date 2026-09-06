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
		result, err := refreshUsageForOutput(context.Background(), client, alias)
		if result.SnapshotId != "" && options.json {
			if err := writeServiceJSON(stdout, result); err != nil {
				return apperrors.New(apperrors.CLIInternal, err)
			}
		} else if result.SnapshotId != "" {
			writeUsageSnapshot(stdout, result)
		}
		return err
	})
}

func refreshUsageForOutput(ctx context.Context, client *httpapi.CommandClient, alias string) (httpapi.UsageSnapshotResponse, error) {
	result, refreshErr := client.RefreshUsage(ctx, alias)
	code := apperrors.Code(refreshErr)
	if code != apperrors.UsageCollectionFailed && code != apperrors.UsageSourceInvalid {
		return result, refreshErr
	}
	latest, err := client.LatestUsage(ctx, alias)
	if err != nil {
		return result, refreshErr
	}
	return latest, refreshErr
}

func writeUsageSnapshot(output io.Writer, snapshot httpapi.UsageSnapshotResponse) {
	fmt.Fprintf(output, "Usage Snapshot: %s (%s)\n", snapshot.Alias, snapshot.Status)
	fmt.Fprintf(output, "Captured: %s from %s %s\n", snapshot.CapturedAt, snapshot.Source, snapshot.SourceVersion)
	observations := make(map[string][]httpapi.UsageObservation, len(snapshot.Observations))
	for _, observation := range snapshot.Observations {
		observations[observation.MetricKey] = append(observations[observation.MetricKey], observation)
	}
	for _, availability := range snapshot.Availability {
		if metricObservations := observations[availability.MetricKey]; len(metricObservations) > 0 {
			for _, observation := range metricObservations {
				fmt.Fprintf(output, "%s: %g %s (%s, %s", observation.MetricKey, observation.Value, observation.Unit, observation.Provenance, observation.Freshness)
				fmt.Fprintf(output, ", age %ds", observation.CaptureAgeSeconds)
				if observation.Availability != "available" {
					fmt.Fprintf(output, ", evidence %s", observation.Availability)
				}
				if observation.WindowStart != "" || observation.WindowEnd != "" {
					fmt.Fprintf(output, ", window %s to %s %s", observation.WindowStart, observation.WindowEnd, observation.WindowTimezone)
				}
				if observation.Assumptions != "" || observation.Uncertainty != "" {
					fmt.Fprintf(output, ", assumptions %s, uncertainty %s", observation.Assumptions, observation.Uncertainty)
				}
				if availability.State != "available" || availability.Reason != "" {
					fmt.Fprintf(output, ", refresh %s: %s at %s", availability.State, availability.Reason, availability.CheckedAt)
				}
				fmt.Fprintln(output, ")")
			}
			continue
		}
		fmt.Fprintf(output, "%s: %s", availability.MetricKey, availability.State)
		if availability.Reason != "" || availability.CheckedAt != "" {
			fmt.Fprintf(output, " (%s, checked %s)", availability.Reason, availability.CheckedAt)
		}
		fmt.Fprintln(output)
	}
}
