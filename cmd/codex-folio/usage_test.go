package main

import (
	"bytes"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/httpapi"
)

func TestWriteUsageSnapshotReportsAvailabilityWithoutSensitivePaths(t *testing.T) {
	result := httpapi.UsageSnapshotResponse{
		SnapshotId: "snapshot-1", ProfileId: "profile-1", Alias: "Work", Source: "codex_app_server", SourceVersion: "0.153.4", CapturedAt: "2026-09-06T12:00:00Z",
		Observations: []httpapi.UsageObservation{{MetricKey: "codex.primary.used_percent", Value: 0, Unit: "percent", Provenance: "Provider-reported Metric", Freshness: "fresh", Availability: "available"}},
		Availability: []httpapi.UsageMetricAvailability{{MetricKey: "codex.primary.used_percent", State: "available"}, {MetricKey: "codex.secondary.used_percent", State: "unsupported"}},
	}
	var output bytes.Buffer
	writeUsageSnapshot(&output, result)
	got := output.String()
	for _, want := range []string{"Usage Snapshot: Work", "codex.primary.used_percent: 0 percent", "codex.secondary.used_percent: unsupported", "Provider-reported Metric"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output = %q, want %q", got, want)
		}
	}
	for _, prohibited := range []string{"CODEX_HOME", "/profiles/work", "cookie", "transcript"} {
		if strings.Contains(got, prohibited) {
			t.Fatalf("output contains prohibited value %q: %q", prohibited, got)
		}
	}
}
