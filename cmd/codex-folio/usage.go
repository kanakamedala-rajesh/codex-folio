package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/usage"
)

func runUsage(args []string, stdout, stderr io.Writer, resolvePaths servicePathResolver) int {
	diagnosticSink := newServiceDiagnosticSink()
	action, alias, scope, options, err := parseUsageRequest(args)
	if err != nil {
		return writeUsageCommandUsage(stderr, diagnosticSink)
	}
	return withSelectionService(os.Stdin, stderr, resolvePaths, openServiceStoreWithVaultMode, diagnosticSink, options, platform.OwnerOptions{}, false, func(client *httpapi.CommandClient) error {
		if action == "show" {
			result, err := client.Analytics(context.Background(), scope)
			if err != nil {
				return err
			}
			if options.json {
				return writeServiceJSON(stdout, result)
			}
			writeAnalytics(stdout, result)
			return nil
		}
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

func writeUsageCommandUsage(stderr io.Writer, diagnosticSink diagnostics.Sink) int {
	recordServiceDiagnostic(diagnosticSink, apperrors.CLIUsage, diagnostics.SeverityWarning)
	fmt.Fprintf(stderr, "codex-folio [%s]: invalid usage arguments\n", apperrors.CLIUsage)
	io.WriteString(stderr, "Usage: codex-folio usage {refresh ALIAS|show [--combined]} [--state-root PATH] [--vault-mode MODE] [--json]\n")
	return exitUsage
}

func parseUsageRequest(args []string) (action, alias, scope string, options selectionOptions, err error) {
	if len(args) == 0 {
		return "", "", "", options, errors.New("usage action is required")
	}
	action = args[0]
	remaining := args[1:]
	switch action {
	case "refresh":
		if len(remaining) == 0 {
			return "", "", "", options, errors.New("refresh requires an alias")
		}
		alias, remaining = remaining[0], remaining[1:]
	case "show":
		filtered := make([]string, 0, len(remaining))
		for _, item := range remaining {
			if item == "--combined" && scope == "" {
				scope = usage.ScopeCombinedIdentity
				continue
			}
			filtered = append(filtered, item)
		}
		remaining = filtered
	default:
		return "", "", "", options, errors.New("unknown usage action")
	}
	options, err = parseSelectionOptions(remaining)
	return action, alias, scope, options, err
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
	fmt.Fprintf(output, "Trigger: %s\n", snapshot.TriggerReason)
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

func writeAnalytics(output io.Writer, result httpapi.AnalyticsResponse) {
	fmt.Fprintf(output, "Dashboard Scope: %s\n", result.Scope)
	fmt.Fprintf(output, "Eligible Profile Count: %d\n", result.EligibleProfileCount)
	for _, candidate := range result.Candidates {
		label := candidate.CapacityState
		if candidate.ProfileId == result.RecommendedProfileId {
			label = "Recommended"
		}
		fmt.Fprintf(output, "Identity Profile: %s (%s; launch eligible: %t)\n", candidate.Alias, label, candidate.Eligible)
	}
	for _, snapshot := range result.Profiles {
		writeUsageSnapshot(output, snapshot)
	}
	for _, aggregate := range result.Aggregates {
		fmt.Fprintf(output, "Combined %s: %g %s across %d profiles\n", aggregate.MetricKey, aggregate.Value, aggregate.Unit, aggregate.ProfileCount)
	}
	for _, ambiguity := range result.Ambiguities {
		fmt.Fprintf(output, "Combined %s: ambiguous (%s)\n", ambiguity.MetricKey, ambiguity.Reason)
	}
	writeActivityTimeline(output, activityRecords(result.Activity))
}

func activityRecords(items []httpapi.ActivityRecord) []activity.TimelineRecord {
	records := make([]activity.TimelineRecord, 0, len(items))
	for _, item := range items {
		startedAt, _ := time.Parse(time.RFC3339Nano, item.StartedAt)
		lastObservedAt, _ := time.Parse(time.RFC3339Nano, item.LastObservedAt)
		record := activity.TimelineRecord{
			RecordType: item.RecordType, ID: item.Id, SourceSessionID: item.SourceSessionId, ProfileID: item.ProfileId, ProfileAlias: item.ProfileAlias,
			ProjectID: item.ProjectId, ProjectAlias: item.ProjectAlias, ProjectBasename: item.ProjectBasename,
			Source: item.Source, SourceVersion: item.SourceVersion, Provenance: item.Provenance,
			StartedAt: startedAt, LastObservedAt: lastObservedAt, Lifecycle: item.Lifecycle, Model: item.Model,
			Correlation: activity.Correlation{State: item.CorrelationState, ManagedLaunchID: item.CorrelationManagedLaunchId, EvidenceType: item.CorrelationEvidenceType, Confidence: item.CorrelationConfidence},
		}
		if value, err := strconv.Atoi(item.ExitStatus); err == nil && item.ExitStatus != "" {
			record.ExitStatus = &value
		}
		if value, err := strconv.ParseInt(item.TokensUsed, 10, 64); err == nil && item.TokensUsed != "" {
			record.TokensUsed = &value
		}
		records = append(records, record)
	}
	return records
}
