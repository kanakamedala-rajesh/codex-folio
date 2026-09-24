package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/usage"
)

func TestAnalyticsExportFiltersNormalizedEvidenceAndDisclosesPathsExplicitly(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	ctx := context.Background()
	addReadyProfile(t, stateStore, "profile-1", "Work")
	addReadyProfile(t, stateStore, "profile-2", "Personal")
	if _, err := stateStore.SelectProfile(ctx, "Work"); err != nil {
		t.Fatal(err)
	}
	project := ProjectIdentity{
		ProjectIdentityID: "project-1", ProjectAlias: "Ledger", RepositoryBasename: "ledger",
		CanonicalPath: filepath.Join(t.TempDir(), "private", "work", `ledger\archive`), CreatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := stateStore.PutProjectIdentity(ctx, project); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	target, err := stateStore.ResolveUsageProfile(ctx, "Work")
	if err != nil {
		t.Fatal(err)
	}
	target.LoginIdentity, target.Workspace = "login-1", "workspace-1"
	metric := usage.Registry()[0]
	observation := usage.Observation{Metric: metric, Value: 25, Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable, ObservedAt: at, CapturedAt: at, WindowTimezone: "UTC"}
	snapshot := usage.Snapshot{Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: at, Status: usage.AvailabilityAvailable, TriggerReason: usage.TriggerExplicitRefresh, Observations: []usage.Observation{observation}, Availability: completeUsageAvailability(at, []usage.MetricAvailability{{MetricKey: metric.Key, State: usage.AvailabilityAvailable, CheckedAt: at, Provenance: usage.ProvenanceProvider}})}
	if _, err := stateStore.SaveUsageSnapshot(ctx, target, snapshot); err != nil {
		t.Fatal(err)
	}
	tx, err := stateStore.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.aggregateObservation(ctx, tx, target.ID, observation, aggregateSourceScope{LoginIdentity: target.LoginIdentity, Workspace: target.Workspace}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE usage_aggregates SET project_identity_id = ?`, project.ProjectIdentityID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.SaveObservedSessions(ctx, []activity.ObservedSessionRecord{
		{SourceSessionID: "session-1", ProfileID: target.ID, ProfileAlias: target.Alias, ProjectID: project.ProjectIdentityID, ProjectAlias: project.ProjectAlias, ProjectBasename: project.RepositoryBasename, Source: activity.SourceLocalMetadata, SourceVersion: "0.153.4", StartedAt: at, LastObservedAt: at.Add(time.Minute), Model: "gpt-5", TokensUsed: int64Pointer(50)},
		{SourceSessionID: "session-outside-range", ProfileID: target.ID, ProfileAlias: target.Alias, ProjectID: project.ProjectIdentityID, ProjectAlias: project.ProjectAlias, ProjectBasename: project.RepositoryBasename, Source: activity.SourceLocalMetadata, SourceVersion: "0.153.4", StartedAt: at.Add(48 * time.Hour), LastObservedAt: at.Add(48*time.Hour + time.Minute)},
	}); err != nil {
		t.Fatal(err)
	}
	const prohibitedSeed = "prompt-response-command-tool-diff-transcript-cookie-credential-secret"
	if _, err := stateStore.db.ExecContext(ctx, `INSERT INTO diagnostic_aggregates (diagnostic_aggregate_id, component, error_code, severity, occurrence_count, first_seen_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, "diagnostic-1", "collector", prohibitedSeed, "error", 1, at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	request := activity.ExportRequest{Format: "json", Datasets: []string{"usage", "availability", "aggregates", "activity"}, Scope: usage.ScopeSelectedProfile, ProfileID: "selected", ProjectID: "*", From: "2026-09-01T00:00:00Z", To: "2026-09-03T00:00:00Z"}
	records, err := stateStore.ExportAnalytics(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if records.Usage == nil || records.Availability == nil || records.Aggregates == nil || records.Activity == nil || len(*records.Usage) != 1 || len(*records.Availability) != len(usage.Registry()) || len(*records.Aggregates) != 1 || len(*records.Activity) != 1 {
		t.Fatalf("export records = %#v", records)
	}
	if (*records.Usage)[0].ProfileID != target.ID || (*records.Usage)[0].LoginIdentity != "login-1" || (*records.Usage)[0].Workspace != "workspace-1" || (*records.Usage)[0].CanonicalPath != "" {
		t.Fatalf("usage record = %#v", (*records.Usage)[0])
	}
	if (*records.Usage)[0].Metric.Unit == "" || (*records.Usage)[0].Source == "" || (*records.Usage)[0].Freshness != usage.FreshnessStale || (*records.Usage)[0].CaptureAgeSeconds != 7200 || (*records.Aggregates)[0].Freshness != usage.FreshnessStale {
		t.Fatalf("detail/aggregate semantics = %#v / %#v", (*records.Usage)[0], (*records.Aggregates)[0])
	}
	partial := false
	for _, availability := range *records.Availability {
		partial = partial || availability.State == usage.AvailabilityUnsupported
	}
	if !partial || (*records.Activity)[0].RecordType == "" || (*records.Activity)[0].Correlation.State == "" {
		t.Fatalf("partial/activity semantics = %#v / %#v", *records.Availability, (*records.Activity)[0])
	}
	encoded, err := json.Marshal(records)
	if err != nil || strings.Contains(string(encoded), prohibitedSeed) || strings.Contains(string(encoded), `"canonical_path"`) {
		t.Fatalf("prohibited export content = %s/%v", encoded, err)
	}
	if (*records.Aggregates)[0].ProjectAlias != "Ledger" || (*records.Aggregates)[0].CanonicalPath != "" || (*records.Activity)[0].ProjectBasename != "ledger" || (*records.Activity)[0].CanonicalPath != "" {
		t.Fatalf("safe records = %#v / %#v", (*records.Aggregates)[0], (*records.Activity)[0])
	}

	request.IncludePaths = true
	records, err = stateStore.ExportAnalytics(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if (*records.Aggregates)[0].CanonicalPath != project.CanonicalPath || (*records.Activity)[0].CanonicalPath != project.CanonicalPath {
		t.Fatalf("explicit paths = %q/%q", (*records.Aggregates)[0].CanonicalPath, (*records.Activity)[0].CanonicalPath)
	}
	encoded, err = json.Marshal(records)
	var decoded activity.ExportRecords
	if err != nil {
		t.Fatalf("explicit path export = %s/%v", encoded, err)
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil || (*decoded.Aggregates)[0].CanonicalPath != project.CanonicalPath || (*decoded.Activity)[0].CanonicalPath != project.CanonicalPath {
		t.Fatalf("explicit path export = %#v/%v", decoded, err)
	}
	request.ProjectID = "none"
	records, err = stateStore.ExportAnalytics(ctx, request)
	if err != nil || len(*records.Usage) != 1 || len(*records.Availability) != len(usage.Registry()) || len(*records.Aggregates) != 0 || len(*records.Activity) != 0 {
		t.Fatalf("project filter = %#v/%v", records, err)
	}
}

func int64Pointer(value int64) *int64 { return &value }

func TestCombinedIdentityActivityExportExcludesUnassignedHistory(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	ctx := context.Background()
	addReadyProfile(t, stateStore, "profile-1", "Work")
	at := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	if err := stateStore.SaveObservedSessions(ctx, []activity.ObservedSessionRecord{
		{SourceSessionID: "linked", ProfileID: "profile-1", Source: activity.SourceLocalMetadata, SourceVersion: "0.153.4", StartedAt: at, LastObservedAt: at},
		{SourceSessionID: "unassigned", Source: activity.SourceLocalMetadata, SourceVersion: "0.153.4", StartedAt: at, LastObservedAt: at},
	}); err != nil {
		t.Fatal(err)
	}
	history, err := stateStore.ListActivity(ctx, activity.Filters{})
	if err != nil || len(history) != 2 {
		t.Fatalf("overall history = %#v/%v", history, err)
	}
	records, err := stateStore.ExportAnalytics(ctx, activity.ExportRequest{
		Format: "json", Datasets: []string{"activity"}, Scope: usage.ScopeCombinedIdentity,
		ProfileID: "*", ProjectID: "*", From: "all", To: "all",
	})
	if err != nil || records.Activity == nil || len(*records.Activity) != 1 || (*records.Activity)[0].SourceSessionID != "linked" {
		t.Fatalf("combined activity export = %#v/%v", records.Activity, err)
	}
}
