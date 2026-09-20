package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/alerts"
	"venkatasudha.com/codex-folio/internal/usage"
)

func TestAlertHistoryDeduplicatesAcknowledgesResolvesAndReopens(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	state, err := OpenWithOptions(Options{Path: filepath.Join(t.TempDir(), "alerts.sqlite3"), Clock: fixedStoreClock{now: now}})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	defer func() { _ = state.Close() }()
	seedAlertProfile(t, state)
	remaining := 9.0
	condition := alerts.Condition{Key: "profile-1|capacity_critical|primary", ProfileID: "profile-1", ProfileAlias: "Work", Category: alerts.CategoryCapacity, Kind: alerts.KindCapacityCritical, Severity: alerts.SeverityError, Title: "Capacity at critical threshold", Guidance: "Refresh", MetricKey: "codex.primary.used_percent", RemainingPercent: &remaining, Source: usage.SourceCodexAppServer, SourceVersion: "2.7.0", Provenance: usage.ProvenanceProvider, Scope: "provider_quota_window", Freshness: usage.FreshnessFresh, AvailabilityReason: usage.ReasonStale, EvidenceCapturedAt: now.Add(-time.Minute), ObservedAt: now.Add(-time.Minute)}

	if err := state.SyncAlerts(context.Background(), "profile-1", []alerts.Condition{condition}, now, alerts.HistoryLimit); err != nil {
		t.Fatalf("SyncAlerts() error = %v", err)
	}
	if err := state.SyncAlerts(context.Background(), "profile-1", []alerts.Condition{condition}, now.Add(time.Minute), alerts.HistoryLimit); err != nil {
		t.Fatalf("second SyncAlerts() error = %v", err)
	}
	records, err := state.ListAlerts(context.Background(), alerts.HistoryLimit)
	if err != nil || len(records) != 1 || records[0].OccurrenceCount != 1 || records[0].State != alerts.StateOpen || !records[0].LastSeenAt.Equal(now) {
		t.Fatalf("ListAlerts() = %#v/%v", records, err)
	}
	condition.EvidenceCapturedAt = now
	condition.ObservedAt = now
	if err := state.SyncAlerts(context.Background(), "profile-1", []alerts.Condition{condition}, now.Add(2*time.Minute), alerts.HistoryLimit); err != nil {
		t.Fatalf("new-evidence SyncAlerts() error = %v", err)
	}
	records, _ = state.ListAlerts(context.Background(), alerts.HistoryLimit)
	if records[0].OccurrenceCount != 2 || !records[0].LastSeenAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("new evidence did not advance occurrence metadata: %#v", records[0])
	}
	if records[0].Source != usage.SourceCodexAppServer || records[0].SourceVersion != "2.7.0" || records[0].Provenance != usage.ProvenanceProvider || records[0].Scope != "provider_quota_window" || records[0].Freshness != usage.FreshnessFresh || records[0].AvailabilityReason != usage.ReasonStale || !records[0].EvidenceCapturedAt.Equal(now) || !records[0].ObservedAt.Equal(now) {
		t.Fatalf("retained evidence = %#v", records[0].Condition)
	}
	if err := state.AcknowledgeAlert(context.Background(), records[0].ID, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("AcknowledgeAlert() error = %v", err)
	}
	if err := state.SyncAlerts(context.Background(), "profile-1", nil, now.Add(4*time.Minute), alerts.HistoryLimit); err != nil {
		t.Fatalf("resolve SyncAlerts() error = %v", err)
	}
	records, _ = state.ListAlerts(context.Background(), alerts.HistoryLimit)
	if records[0].State != alerts.StateResolved || records[0].AcknowledgedAt == nil || records[0].ResolvedAt == nil {
		t.Fatalf("resolved record = %#v", records[0])
	}
	if err := state.SyncAlerts(context.Background(), "profile-1", []alerts.Condition{condition}, now.Add(5*time.Minute), alerts.HistoryLimit); err != nil {
		t.Fatalf("reopen SyncAlerts() error = %v", err)
	}
	records, _ = state.ListAlerts(context.Background(), alerts.HistoryLimit)
	if records[0].State != alerts.StateOpen || records[0].AcknowledgedAt != nil || records[0].ResolvedAt != nil || records[0].OccurrenceCount != 3 {
		t.Fatalf("reopened record = %#v", records[0])
	}
}

func TestAlertHistoryPrunesResolvedRecordsToBound(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	state, err := Open(filepath.Join(t.TempDir(), "alerts.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	seedAlertProfile(t, state)
	for index := 0; index < 3; index++ {
		condition := alerts.Condition{Key: fmt.Sprintf("condition-%d", index), ProfileID: "profile-1", Category: alerts.CategoryCompatibility, Kind: alerts.KindCompatibilityChanged, Severity: alerts.SeverityWarning, Title: "Changed", Guidance: "Refresh", ObservedAt: now.Add(time.Duration(index) * time.Minute)}
		at := now.Add(time.Duration(index*2) * time.Minute)
		if err := state.SyncAlerts(context.Background(), "profile-1", []alerts.Condition{condition}, at, 2); err != nil {
			t.Fatal(err)
		}
		if err := state.SyncAlerts(context.Background(), "profile-1", nil, at.Add(time.Minute), 2); err != nil {
			t.Fatal(err)
		}
	}
	records, err := state.ListAlerts(context.Background(), 10)
	if err != nil || len(records) != 2 {
		t.Fatalf("bounded history = %#v/%v", records, err)
	}
	for _, record := range records {
		if record.Key == "condition-0" {
			t.Fatalf("oldest resolved alert was retained: %#v", records)
		}
	}
}

func TestListAlertsReturnsEveryActiveConditionAndBoundsResolvedHistory(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	state, err := Open(filepath.Join(t.TempDir(), "alerts.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	seedAlertProfile(t, state)
	if err := state.SyncAlerts(context.Background(), "", []alerts.Condition{
		{Key: "resolved-1", Category: alerts.CategoryCompatibility, Kind: alerts.KindCompatibilityChanged, Severity: alerts.SeverityWarning, Title: "Changed", Guidance: "Refresh", ObservedAt: now},
		{Key: "resolved-2", Category: alerts.CategoryCompatibility, Kind: alerts.KindCompatibilityChanged, Severity: alerts.SeverityWarning, Title: "Changed", Guidance: "Refresh", ObservedAt: now.Add(time.Minute)},
	}, now, 2); err != nil {
		t.Fatal(err)
	}
	if err := state.SyncAlerts(context.Background(), "", nil, now.Add(time.Minute), 2); err != nil {
		t.Fatal(err)
	}
	conditions := make([]alerts.Condition, 0, 4)
	for index := 0; index < 4; index++ {
		conditions = append(conditions, alerts.Condition{Key: fmt.Sprintf("active-%d", index), ProfileID: "profile-1", Category: alerts.CategoryCompatibility, Kind: alerts.KindCompatibilityChanged, Severity: alerts.SeverityWarning, Title: "Changed", Guidance: "Refresh", EvidenceCapturedAt: now.Add(time.Duration(index) * time.Minute), ObservedAt: now.Add(time.Duration(index) * time.Minute)})
	}
	if err := state.SyncAlerts(context.Background(), "profile-1", conditions, now.Add(2*time.Minute), 2); err != nil {
		t.Fatal(err)
	}
	records, err := state.ListAlerts(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	active, resolved := 0, 0
	for _, record := range records {
		if record.State == alerts.StateResolved {
			resolved++
		} else {
			active++
		}
	}
	if active != 4 || resolved != 2 {
		t.Fatalf("ListAlerts() returned %d active/%d resolved records: %#v", active, resolved, records)
	}
}

func TestAlertThresholdsArePerProfileAndWindow(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	state, err := Open(filepath.Join(t.TempDir(), "alerts.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	seedAlertProfile(t, state)
	for _, threshold := range []alerts.Threshold{
		{ProfileID: "profile-1", MetricKey: "codex.primary.used_percent", WarningPercent: 25, CriticalPercent: 12},
		{ProfileID: "profile-1", MetricKey: "codex.secondary.used_percent", WarningPercent: 18, CriticalPercent: 8},
	} {
		if err := state.SetAlertThreshold(context.Background(), threshold, now); err != nil {
			t.Fatalf("SetAlertThreshold(%#v) error = %v", threshold, err)
		}
	}
	thresholds, err := state.AlertThresholds(context.Background())
	if err != nil || len(thresholds) != 2 || thresholds[0].WarningPercent != 25 || thresholds[1].WarningPercent != 18 {
		t.Fatalf("AlertThresholds() = %#v/%v", thresholds, err)
	}
}

func TestAlertEvidenceIncludesProfilesWithoutUsage(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "alerts.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	seedAlertProfile(t, state)
	evidence, err := state.AlertEvidence(context.Background(), "")
	if err != nil || len(evidence) != 1 || evidence[0].ProfileID != "profile-1" || evidence[0].ConsecutiveFailures != 0 {
		t.Fatalf("AlertEvidence() = %#v/%v", evidence, err)
	}
}

func TestAlertEvidenceComparesConsecutiveUsefulSourceVersions(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	state, err := Open(filepath.Join(t.TempDir(), "alerts.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	seedAlertProfile(t, state)
	target := usage.ProfileTarget{ID: "profile-1", Alias: "Work"}
	metric := usage.Registry()[0]
	saveUseful := func(version string, capturedAt time.Time) {
		windowStart, windowEnd := capturedAt.Add(-time.Hour), capturedAt.Add(time.Hour)
		_, err := state.SaveUsageSnapshot(context.Background(), target, usage.Snapshot{Source: usage.SourceCodexAppServer, SourceVersion: version, CapturedAt: capturedAt, Status: usage.AvailabilityAvailable, TriggerReason: usage.TriggerExplicitRefresh,
			Observations: []usage.Observation{{Metric: metric, Value: 50, ObservedAt: capturedAt, CapturedAt: capturedAt, WindowStart: &windowStart, WindowEnd: &windowEnd, WindowTimezone: "UTC", Source: usage.SourceCodexAppServer, SourceVersion: version, Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable}},
			Availability: completeUsageAvailability(capturedAt, []usage.MetricAvailability{{MetricKey: metric.Key, State: usage.AvailabilityAvailable, CheckedAt: capturedAt, Provenance: usage.ProvenanceProvider}})})
		if err != nil {
			t.Fatalf("SaveUsageSnapshot(%q): %v", version, err)
		}
	}
	saveUseful("1.0", now.Add(-3*time.Minute))
	failed := usage.NewUnavailableSnapshot("failure-marker", now.Add(-2*time.Minute), usage.AvailabilityTemporarilyUnavailable, usage.ReasonCollectionFailed)
	failed.TriggerReason = usage.TriggerPeriodicActive
	if _, err := state.SaveUsageSnapshot(context.Background(), target, failed); err != nil {
		t.Fatalf("save failed capture: %v", err)
	}
	saveUseful("2.0", now.Add(-time.Minute))
	evidence, err := state.AlertEvidence(context.Background(), "profile-1")
	if err != nil || len(evidence) != 1 || evidence[0].Snapshot.SourceVersion != "2.0" || evidence[0].PreviousSourceVersion != "1.0" || evidence[0].ConsecutiveFailures != 0 {
		t.Fatalf("AlertEvidence() = %#v/%v", evidence, err)
	}
}

func TestOperationalAlertsMigrationPreservesExistingAlert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema-v19.sqlite3")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, candidate := range migrations() {
		if candidate.version > 19 {
			break
		}
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := candidate.apply(ctx, tx); err != nil {
			_ = tx.Rollback()
			t.Fatalf("apply migration %d: %v", candidate.version, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)", candidate.version, candidate.name, "2026-09-19T12:00:00Z"); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", candidate.version)); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO alerts (alert_id, category, severity, state, created_at) VALUES ('legacy-alert', 'capacity', 'warning', 'open', '2026-09-19T12:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := Open(path)
	if err != nil {
		t.Fatalf("Open(v19): %v", err)
	}
	defer func() { _ = state.Close() }()
	records, err := state.ListAlerts(ctx, 10)
	if err != nil || len(records) != 1 || records[0].ID != "legacy-alert" || records[0].Key != "legacy-alert" || records[0].OccurrenceCount != 1 || records[0].State != alerts.StateOpen {
		t.Fatalf("migrated alert = %#v/%v", records, err)
	}
}

func TestNotificationPreferenceAndDeliveryClaimPersistWithoutDuplicateDelivery(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "notifications.sqlite3")
	state, err := OpenWithOptions(Options{Path: path, Clock: fixedStoreClock{now: now}})
	if err != nil {
		t.Fatal(err)
	}
	seedAlertProfile(t, state)
	preference, err := state.NotificationPreference(context.Background())
	if err != nil || preference.DetailEnabled {
		t.Fatalf("default preference = %#v/%v", preference, err)
	}
	preference, err = state.SetNotificationDetail(context.Background(), true, now)
	if err != nil || !preference.DetailEnabled {
		t.Fatalf("SetNotificationDetail() = %#v/%v", preference, err)
	}
	condition := alerts.Condition{Key: "profile-1|reauthentication", ProfileID: "profile-1", Category: alerts.CategoryReauthentication, Kind: alerts.KindReauthentication, Severity: alerts.SeverityError, Title: "Reauthentication required", Guidance: "Reauthenticate", ObservedAt: now}
	if err := state.SyncAlerts(context.Background(), "profile-1", []alerts.Condition{condition}, now, alerts.HistoryLimit); err != nil {
		t.Fatal(err)
	}
	records, _ := state.ListAlerts(context.Background(), alerts.HistoryLimit)
	if len(records) != 1 || records[0].DeliveryState != alerts.DeliveryPending || records[0].DeliveryAttempts != 0 {
		t.Fatalf("new delivery = %#v", records)
	}
	claimed, err := state.ClaimAlertDelivery(context.Background(), records[0].ID, now)
	if err != nil || !claimed {
		t.Fatalf("ClaimAlertDelivery() = %v/%v", claimed, err)
	}
	claimed, err = state.ClaimAlertDelivery(context.Background(), records[0].ID, now)
	if err != nil || claimed {
		t.Fatalf("duplicate ClaimAlertDelivery() = %v/%v", claimed, err)
	}
	if err := state.RecordAlertDelivery(context.Background(), records[0].ID, alerts.DeliveryOutcome{State: alerts.DeliveryDelivered, Attempts: 1, AttemptedAt: now, DeliveredAt: &now}); err != nil {
		t.Fatal(err)
	}
	if err := state.SyncAlerts(context.Background(), "profile-1", []alerts.Condition{condition}, now.Add(time.Minute), alerts.HistoryLimit); err != nil {
		t.Fatal(err)
	}
	records, _ = state.ListAlerts(context.Background(), alerts.HistoryLimit)
	if records[0].DeliveryState != alerts.DeliveryDelivered || records[0].DeliveryAttempts != 1 {
		t.Fatalf("repeated observation reset delivery = %#v", records[0])
	}
	if err := state.SyncAlerts(context.Background(), "profile-1", nil, now.Add(2*time.Minute), alerts.HistoryLimit); err != nil {
		t.Fatal(err)
	}
	if err := state.SyncAlerts(context.Background(), "profile-1", []alerts.Condition{condition}, now.Add(3*time.Minute), alerts.HistoryLimit); err != nil {
		t.Fatal(err)
	}
	records, _ = state.ListAlerts(context.Background(), alerts.HistoryLimit)
	if records[0].DeliveryState != alerts.DeliveryPending || records[0].DeliveryAttempts != 0 || records[0].DeliveredAt != nil {
		t.Fatalf("reopened delivery = %#v", records[0])
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	preference, err = reopened.NotificationPreference(context.Background())
	if err != nil || !preference.DetailEnabled {
		t.Fatalf("persisted preference = %#v/%v", preference, err)
	}
}

func TestNativeAlertDeliveryMigrationDefaultsToGenericPendingState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema-v20.sqlite3")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, candidate := range migrations() {
		if candidate.version > 20 {
			break
		}
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := candidate.apply(ctx, tx); err != nil {
			_ = tx.Rollback()
			t.Fatalf("apply migration %d: %v", candidate.version, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)", candidate.version, candidate.name, "2026-09-19T12:00:00Z"); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", candidate.version)); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO alerts (alert_id, condition_key, category, kind, severity, state, title, guidance, observed_at, first_seen_at, last_seen_at, occurrence_count) VALUES ('alert-1', 'condition-1', 'compatibility', 'compatibility_changed', 'warning', 'open', 'Compatibility changed', 'Refresh', '2026-09-19T12:00:00Z', '2026-09-19T12:00:00Z', '2026-09-19T12:00:00Z', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := Open(path)
	if err != nil {
		t.Fatalf("Open(v20): %v", err)
	}
	defer func() { _ = state.Close() }()
	preference, err := state.NotificationPreference(ctx)
	if err != nil || preference.DetailEnabled {
		t.Fatalf("migrated preference = %#v/%v", preference, err)
	}
	records, err := state.ListAlerts(ctx, 10)
	if err != nil || len(records) != 1 || records[0].DeliveryState != alerts.DeliveryPending || records[0].DeliveryAttempts != 0 {
		t.Fatalf("migrated delivery = %#v/%v", records, err)
	}
}

func seedAlertProfile(t *testing.T, state *Store) {
	t.Helper()
	if _, err := state.db.Exec(`INSERT INTO identity_profiles (profile_id, display_name, status, created_at, updated_at) VALUES ('profile-1', 'Work', 'ready', '2026-09-19T12:00:00Z', '2026-09-19T12:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.Exec(`INSERT INTO cli_aliases (alias_id, profile_id, alias, created_at) VALUES ('alias-1', 'profile-1', 'Work', '2026-09-19T12:00:00Z')`); err != nil {
		t.Fatal(err)
	}
}
