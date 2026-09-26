package httpapi

import (
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/usage"
)

func TestHistoricalMetricsSeparateScopesVersionsAndAbsentValues(t *testing.T) {
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	measured := int64(0)
	more := int64(42)
	records := []activity.TimelineRecord{
		{RecordType: activity.RecordTypeObservedSession, Source: activity.SourceLocalMetadata, SourceVersion: "state_5", ProfileID: "work", StartedAt: start, LastObservedAt: start.Add(time.Minute), TokensUsed: &measured},
		{RecordType: activity.RecordTypeObservedSession, Source: activity.SourceLocalMetadata, SourceVersion: "state_5", ProfileID: "", StartedAt: start.Add(time.Hour), LastObservedAt: start.Add(time.Hour + time.Minute), TokensUsed: &more},
		{RecordType: activity.RecordTypeObservedSession, Source: activity.SourceLocalMetadata, SourceVersion: "state_5", ProfileID: "", StartedAt: start.Add(2 * time.Hour), LastObservedAt: start.Add(2 * time.Hour)},
		{RecordType: activity.RecordTypeObservedSession, Source: activity.SourceLocalMetadata, SourceVersion: "state_6", ProfileID: "work", StartedAt: start, LastObservedAt: start.Add(time.Minute), TokensUsed: &more},
		{RecordType: activity.RecordTypeManagedLaunch, Source: "codex_folio", ProfileID: "work", StartedAt: start, LastObservedAt: start.Add(time.Minute)},
	}
	view := usage.DashboardView{Candidates: []usage.Candidate{{ProfileID: "work"}}}
	selected := historicalMetrics(records, view, usage.ScopeSelectedProfile)
	if len(selected) != 2 || selected[0].SessionCount != 1 || selected[0].Value == nil || *selected[0].Value != "0" || selected[0].UnassignedSessionCount != 0 || selected[0].UnassignedValue != nil {
		t.Fatalf("selected metrics = %#v", selected)
	}
	combined := historicalMetrics(records, view, usage.ScopeCombinedIdentity)
	if len(combined) != 2 || combined[0].SessionCount != 1 || combined[0].UnassignedValue != nil {
		t.Fatalf("combined metrics = %#v", combined)
	}
	overall := historicalMetrics(records, view, usage.ScopeOverallHistory)
	if len(overall) != 2 || overall[0].SessionCount != 3 || overall[0].MeasuredSessionCount != 2 || overall[0].Availability != "partial" || overall[0].Value == nil || *overall[0].Value != "42" || overall[0].UnassignedValue == nil || *overall[0].UnassignedValue != "42" || overall[0].UnassignedSessionCount != 2 || overall[1].SessionCount != 1 {
		t.Fatalf("overall metrics = %#v", overall)
	}
	projected := ActivityResponseFor(records)
	if len(projected.Records[0].HistoricalMetrics) != 1 || projected.Records[0].HistoricalMetrics[0].Availability != "available" || projected.Records[2].HistoricalMetrics[0].Availability != "absent" || projected.Records[4].HistoricalMetrics == nil || len(projected.Records[4].HistoricalMetrics) != 0 {
		t.Fatalf("session projections = %#v", projected.Records)
	}
}

func TestHistoricalMetricsAbsentUnassignedValueDiffersFromMeasuredZero(t *testing.T) {
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	zero := int64(0)
	records := []activity.TimelineRecord{
		{RecordType: activity.RecordTypeObservedSession, Source: activity.SourceLocalMetadata, SourceVersion: "state_5", ProfileID: "work", StartedAt: start, LastObservedAt: start, TokensUsed: &zero},
		{RecordType: activity.RecordTypeObservedSession, Source: activity.SourceLocalMetadata, SourceVersion: "state_5", StartedAt: start, LastObservedAt: start},
	}
	view := usage.DashboardView{Candidates: []usage.Candidate{{ProfileID: "work"}}}
	metric := historicalMetrics(records, view, usage.ScopeOverallHistory)[0]
	if metric.Value == nil || *metric.Value != "0" || metric.UnassignedValue != nil || metric.UnassignedSessionCount != 1 {
		t.Fatalf("absent Unassigned measurement = %#v", metric)
	}
	records[1].TokensUsed = &zero
	metric = historicalMetrics(records, view, usage.ScopeOverallHistory)[0]
	if metric.UnassignedValue == nil || *metric.UnassignedValue != "0" {
		t.Fatalf("measured Unassigned zero = %#v", metric)
	}
}
