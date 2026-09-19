package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/continuation"
	"venkatasudha.com/codex-folio/internal/usage"
)

func TestCheckpointRetentionPersistsIndependently(t *testing.T) {
	state, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	ctx := context.Background()
	policy, err := state.CheckpointRetention(ctx)
	if err != nil || policy.RepositoryFirst != "30" || policy.TranscriptAssisted != "7" {
		t.Fatalf("defaults: %#v/%v", policy, err)
	}
	if _, err := state.SetCheckpointRetention(ctx, continuation.SourceRepositoryFirst, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.SetCheckpointRetention(ctx, continuation.SourceTranscriptAssisted, "unlimited"); err != nil {
		t.Fatal(err)
	}
	path, secureVault := state.path, state.vault
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = OpenWithVault(path, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	policy, err = state.CheckpointRetention(ctx)
	if err != nil || policy.RepositoryFirst != "1" || policy.TranscriptAssisted != "unlimited" {
		t.Fatalf("restart: %#v/%v", policy, err)
	}
	analytics, err := state.AnalyticsRetention(ctx)
	if err != nil || analytics.String() != usage.DefaultRetention {
		t.Fatalf("analytics retention changed: %#v/%v", analytics, err)
	}
	if _, err := state.SetCheckpointRetention(ctx, continuation.SourceRepositoryFirst, "0"); err == nil {
		t.Fatal("accepted zero-day retention")
	}
}

func TestAnalyticsRetentionPersistsIndependently(t *testing.T) {
	state, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	ctx := context.Background()
	policy, err := state.AnalyticsRetention(ctx)
	if err != nil || policy.String() != usage.DefaultRetention {
		t.Fatalf("default: %v/%v", policy, err)
	}
	for _, setting := range []string{"30", "unlimited", usage.DefaultRetention} {
		if _, err := state.SetAnalyticsRetention(ctx, setting); err != nil {
			t.Fatal(err)
		}
		path, secureVault := state.path, state.vault
		if err := state.Close(); err != nil {
			t.Fatal(err)
		}
		state, err = OpenWithVault(path, secureVault)
		if err != nil {
			t.Fatal(err)
		}
		policy, err = state.AnalyticsRetention(ctx)
		if err != nil || policy.String() != setting {
			t.Fatalf("restart: %v/%v", policy, err)
		}
	}
	if _, err := state.SetAnalyticsRetention(ctx, "29"); err == nil {
		t.Fatal("accepted less than 30 days")
	}
}

func historyReading(at time.Time) usage.Snapshot {
	snapshot := usage.NewUnavailableSnapshot("0.153.4", at, usage.AvailabilityUnsupported, usage.ReasonUnsupported)
	snapshot.TriggerReason, snapshot.Status = usage.TriggerExplicitRefresh, usage.AvailabilityPartial
	snapshot.Availability[0].State, snapshot.Availability[0].Reason = usage.AvailabilityAvailable, ""
	snapshot.Observations = []usage.Observation{{Metric: usage.Registry()[0], Value: 25, ObservedAt: at, CapturedAt: at, Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable}}
	return snapshot
}

func TestUsageHistoryCombinesRetainedDetailsWithCompactedAggregates(t *testing.T) {
	state, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	addReadyProfile(t, state, "work", "Work")
	target, err := state.ResolveUsageProfile(ctx, "Work")
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{
		time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
	} {
		if _, err := state.SaveUsageSnapshot(ctx, target, historyReading(at)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := state.SetAnalyticsRetention(ctx, "30"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RetainAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	scope := usage.HistoryScope{ProfileID: "work", ProjectID: "*", From: "all", To: "all", Classes: []string{"aggregates"}}
	items, err := state.ListUsageHistory(ctx, scope)
	if err != nil || len(items) != 2 || items[0].Samples != 1 || items[1].Samples != 1 {
		t.Fatalf("combined history = %#v/%v, want one retained detail and one compacted aggregate", items, err)
	}
}

func TestRetentionMinimumDefaultUnlimitedAndExactBoundary(t *testing.T) {
	for _, setting := range []string{"30", usage.DefaultRetention} {
		t.Run(setting, func(t *testing.T) {
			state, err := openProfileTestStore(t)
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			ctx := context.Background()
			addReadyProfile(t, state, "work", "Work")
			target, err := state.ResolveUsageProfile(ctx, "Work")
			if err != nil {
				t.Fatal(err)
			}
			boundary := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
			if setting == usage.DefaultRetention {
				boundary = time.Date(2025, 8, 2, 12, 0, 0, 0, time.UTC)
			}
			for _, at := range []time.Time{boundary.Add(-time.Nanosecond), boundary, boundary.Add(time.Nanosecond)} {
				if _, err := state.SaveUsageSnapshot(ctx, target, historyReading(at)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := state.SetAnalyticsRetention(ctx, "unlimited"); err != nil {
				t.Fatal(err)
			}
			if result, err := state.RetainAnalytics(ctx); err != nil || result.Processed != 0 {
				t.Fatalf("unlimited: %#v/%v", result, err)
			}
			if _, err := state.SetAnalyticsRetention(ctx, setting); err != nil {
				t.Fatal(err)
			}
			if _, err := state.RetainAnalytics(ctx); err != nil {
				t.Fatal(err)
			}
			aggregates, err := state.ListUsageAggregates(ctx, usage.HistoryScope{ProfileID: "work", ProjectID: "none", From: "all", To: "all", Classes: []string{"aggregates"}})
			if err != nil || len(aggregates) != 1 || aggregates[0].Samples != 1 || !aggregates[0].LastObservedAt.Equal(boundary.Add(-time.Nanosecond)) {
				t.Fatalf("boundary aggregate: %#v/%v", aggregates, err)
			}
			var details int
			if err := state.db.QueryRow(`SELECT COUNT(*) FROM usage_observations`).Scan(&details); err != nil || details != 2 {
				t.Fatalf("boundary details: %d/%v", details, err)
			}
		})
	}
}

func TestRetentionSourceScopeAndProvenanceStayDistinct(t *testing.T) {
	state, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	addReadyProfile(t, state, "work", "Work")
	target, err := state.ResolveUsageProfile(ctx, "Work")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2025, 7, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		target.LoginIdentity, target.Workspace = "private-login-fixture", "private-workspace-a"
		if i == 2 {
			target.Workspace = "private-workspace-b"
		}
		snapshot := historyReading(at.Add(time.Duration(i) * time.Minute))
		if i == 3 {
			snapshot.Observations[0].Provenance = usage.ProvenanceEstimated
			snapshot.Observations[0].Assumptions = "provider updates may lag"
		}
		if _, err := state.SaveUsageSnapshot(ctx, target, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := state.RetainAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	scope := usage.HistoryScope{ProfileID: "*", ProjectID: "*", From: "all", To: "all", Classes: []string{"aggregates"}}
	aggregates, err := state.ListUsageAggregates(ctx, scope)
	if err != nil || len(aggregates) != 3 {
		t.Fatalf("source-distinct aggregates: %#v/%v", aggregates, err)
	}
	var readings int64
	for _, a := range aggregates {
		readings += a.Samples
		if a.Value != 25 || a.LoginIdentity != "private-login-fixture" || a.Timezone != "UTC" || a.BucketKind != "calendar_day" {
			t.Fatal("compaction lost normalized source/window semantics")
		}
		if a.Provenance == usage.ProvenanceEstimated && a.Assumptions == "" {
			t.Fatal("estimate lost assumptions")
		}
	}
	if readings != 4 {
		t.Fatalf("reading count: %d", readings)
	}
	encoded, err := json.Marshal(aggregates)
	if err != nil || strings.Contains(string(encoded), "private-") {
		t.Fatal("aggregate projection leaked private source scope")
	}
	// A narrower date selection must not delete a partially intersecting bucket.
	scope.From, scope.To = "2025-07-01T12:00:00Z", "2025-07-01T13:00:00Z"
	preview, err := state.PurgeAnalytics(ctx, scope, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range preview.Counts {
		if count.Count != 0 {
			t.Fatal("partial bucket selected for deletion")
		}
	}
	scope.From, scope.To = "2025-07-01T00:00:00Z", "2025-07-02T00:00:00Z"
	preview, err = state.PurgeAnalytics(ctx, scope, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.PurgeAnalytics(ctx, scope, preview.Confirmation); err != nil {
		t.Fatal(err)
	}
	if after, err := state.ListUsageAggregates(ctx, scope); err != nil || len(after) != 0 {
		t.Fatal("complete aggregate buckets were not purged")
	}
}

func TestRepresentativeHistoryBoundsWritesAndPurge(t *testing.T) {
	state, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	ctx := context.Background()
	addReadyProfile(t, state, "work", "Work")
	target, err := state.ResolveUsageProfile(ctx, "Work")
	if err != nil {
		t.Fatal(err)
	}
	// 400 daily readings span thirteen months; 201 older readings force restarts
	// between maintenance batches. This fixture is evidence, not a user guarantee.
	for i := 0; i < 601; i++ {
		at := time.Date(2025, 8, 2, 12, 0, 0, 0, time.UTC).AddDate(0, 0, i-201)
		if _, err := state.SaveUsageSnapshot(ctx, target, historyReading(at)); err != nil {
			t.Fatal(err)
		}
	}
	scope := usage.HistoryScope{ProfileID: "work", ProjectID: "*", From: "all", To: "all", Classes: []string{"usage", "aggregates"}}
	preview, err := state.PurgeAnalytics(ctx, scope, "")
	if err != nil || preview.Executable {
		t.Fatalf("oversized preview: %#v/%v", preview, err)
	}
	if _, err := state.PurgeAnalytics(ctx, scope, preview.Confirmation); apperrors.Code(err) != apperrors.AnalyticsScopeTooLarge {
		t.Fatalf("oversized purge: %v", err)
	}
	var maxWrites int64
	for batch := 0; ; batch++ {
		if batch > 20 {
			t.Fatal("retention did not converge")
		}
		var before, after int64
		if err := state.db.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		result, err := state.RetainAnalytics(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.db.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
			t.Fatal(err)
		}
		writes := after - before
		maxWrites = max(maxWrites, writes)
		if writes > int64(usage.RetentionBatchSize*8) {
			t.Fatalf("unbounded batch: %d writes", writes)
		}
		if !result.More {
			break
		}
		path, secureVault, clock := state.path, state.vault, state.clock
		if err := state.Close(); err != nil {
			t.Fatal(err)
		}
		state, err = OpenWithOptions(Options{Path: path, Vault: secureVault, Clock: clock})
		if err != nil {
			t.Fatal(err)
		}
	}
	var detailCount, aggregateSamples int
	if err := state.db.QueryRow(`SELECT COUNT(*) FROM usage_observations`).Scan(&detailCount); err != nil {
		t.Fatal(err)
	}
	if err := state.db.QueryRow(`SELECT SUM(samples) FROM usage_aggregates`).Scan(&aggregateSamples); err != nil {
		t.Fatal(err)
	}
	if detailCount != 400 || aggregateSamples != 201 {
		t.Fatalf("restart lost/doubled history: detail=%d aggregate samples=%d", detailCount, aggregateSamples)
	}
	started := time.Now()
	latest, err := state.LatestUsageSnapshot(ctx, target)
	elapsed := time.Since(started)
	if err != nil || len(latest.Observations) != 1 {
		t.Fatalf("default-history query: %#v/%v", latest, err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("representative query exceeded engineering budget: %s", elapsed)
	}
	t.Logf("601 readings; max retention batch writes=%d; latest-history query=%s (local fixture, not a guarantee)", maxWrites, elapsed)
}

func TestRetentionAggregatesDistinctEvidenceAtomically(t *testing.T) {
	state, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	addReadyProfile(t, state, "profile-1", "Work")
	target, err := state.ResolveUsageProfile(context.Background(), "Work")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2025, 7, 1, 12, 0, 0, 0, time.UTC)
	start, end := at.Add(-time.Hour), at.Add(time.Hour)
	for i := 0; i < 3; i++ {
		snapshot := usage.NewUnavailableSnapshot("0.153.4", at.Add(time.Duration(i)*time.Minute), usage.AvailabilityUnsupported, usage.ReasonUnsupported)
		snapshot.TriggerReason = usage.TriggerExplicitRefresh
		snapshot.Status = usage.AvailabilityPartial
		snapshot.Availability[0].State, snapshot.Availability[0].Reason = usage.AvailabilityAvailable, ""
		snapshot.Observations = []usage.Observation{{Metric: usage.Registry()[0], Value: 25, ObservedAt: snapshot.CapturedAt, CapturedAt: snapshot.CapturedAt, Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable, WindowStart: &start, WindowEnd: &end, WindowTimezone: "Asia/Kolkata"}}
		if _, err := state.SaveUsageSnapshot(context.Background(), target, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := state.db.Exec(`CREATE TRIGGER interrupt_compaction BEFORE DELETE ON usage_observations BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RetainAnalytics(context.Background()); err == nil {
		t.Fatal("injected compaction failure succeeded")
	}
	scope := usage.HistoryScope{ProfileID: "*", ProjectID: "*", From: "all", To: "all", Classes: []string{"aggregates"}}
	if aggregates, err := state.ListUsageAggregates(context.Background(), scope); err != nil || len(aggregates) != 0 {
		t.Fatal("failed compaction leaked an aggregate")
	}
	if _, err := state.db.Exec(`DROP TRIGGER interrupt_compaction`); err != nil {
		t.Fatal(err)
	}
	path, secureVault, clock := state.path, state.vault, state.clock
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = OpenWithOptions(Options{Path: path, Vault: secureVault, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	result, err := state.RetainAnalytics(context.Background())
	if err != nil || result.Processed == 0 {
		t.Fatalf("retention: %#v/%v", result, err)
	}
	aggregates, err := state.ListUsageAggregates(context.Background(), scope)
	if err != nil || len(aggregates) != 1 {
		t.Fatalf("aggregates: %#v/%v", aggregates, err)
	}
	a := aggregates[0]
	if a.Value != 25 || a.Samples != 3 || a.BucketKind != "source_window" || !a.BucketStart.Equal(start) || !a.BucketEnd.Equal(end) || a.Timezone != "Asia/Kolkata" || a.Provenance != usage.ProvenanceProvider {
		t.Fatalf("aggregate semantics: %#v", a)
	}
	again, err := state.RetainAnalytics(context.Background())
	if err != nil || again.Processed != 0 {
		t.Fatalf("repeated retention: %#v/%v", again, err)
	}
	latest, err := state.LatestUsageSnapshot(context.Background(), target)
	if err != nil || len(latest.Observations) != 1 || latest.Observations[0].Value != 25 {
		t.Fatalf("last-known evidence: %#v/%v", latest, err)
	}
}
