package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/usage"
)

func TestCollectionSettingsPersistDefaultsAndRejectUnsafeIntervals(t *testing.T) {
	state, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	path, secureVault := state.path, state.vault
	settings, err := state.CollectionSettings(context.Background())
	if err != nil || settings != usage.DefaultCollectionSettings() {
		t.Fatalf("CollectionSettings() = %#v, %v", settings, err)
	}
	if _, err := state.SetCollectionSettings(context.Background(), usage.CollectionSettings{ActiveInterval: 4 * time.Minute, IdleInterval: 30 * time.Minute}); err == nil {
		t.Fatal("SetCollectionSettings() accepted an interval below the provider floor")
	}
	want := usage.CollectionSettings{ActiveInterval: 10 * time.Minute, IdleInterval: 45 * time.Minute, ProviderMinimum: usage.ProviderSafeMinimum}
	if got, err := state.SetCollectionSettings(context.Background(), want); err != nil || got != want {
		t.Fatalf("SetCollectionSettings() = %#v, %v", got, err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = OpenWithVault(path, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	if got, err := state.CollectionSettings(context.Background()); err != nil || got != want {
		t.Fatalf("reopened CollectionSettings() = %#v, %v", got, err)
	}
}

func TestPeriodicCollectionMigrationPreservesReferencedSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema-v18.sqlite3")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, candidate := range migrations() {
		if candidate.version > 18 {
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
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)", candidate.version, candidate.name, "2026-09-18T12:00:00Z"); err != nil {
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
	if _, err := database.ExecContext(ctx, `INSERT INTO identity_profiles (profile_id, display_name, status, created_at, updated_at) VALUES ('profile-1', 'Work', 'ready', '2026-09-18T12:00:00Z', '2026-09-18T12:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO usage_snapshots (snapshot_id, profile_id, source, source_version, captured_at, status, trigger_reason) VALUES ('snapshot-1', 'profile-1', 'codex_app_server', '1.0', '2026-09-18T12:00:00Z', 'available', 'explicit_refresh')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO usage_metrics (metric_key, unit, value_kind, created_at) VALUES ('quota.primary.used_percent', 'percent', 'percentage', '2026-09-18T12:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO usage_observations (observation_id, profile_id, metric_key, value, unit, observed_at, snapshot_id) VALUES ('observation-1', 'profile-1', 'quota.primary.used_percent', 42, 'percent', '2026-09-18T12:00:00Z', 'snapshot-1')`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	state, err := Open(path)
	if err != nil {
		t.Fatalf("Open(v18) migration error = %v", err)
	}
	defer state.Close()
	var trigger string
	if err := state.db.QueryRowContext(ctx, "SELECT trigger_reason FROM usage_snapshots WHERE snapshot_id = 'snapshot-1'").Scan(&trigger); err != nil {
		t.Fatal(err)
	}
	if trigger != "explicit_refresh" {
		t.Fatalf("preserved trigger = %q", trigger)
	}
	var snapshotID string
	var value float64
	if err := state.db.QueryRowContext(ctx, "SELECT snapshot_id, value FROM usage_observations WHERE observation_id = 'observation-1'").Scan(&snapshotID, &value); err != nil {
		t.Fatal(err)
	}
	if snapshotID != "snapshot-1" || value != 42 {
		t.Fatalf("preserved observation = snapshot %q, value %v", snapshotID, value)
	}
	var foreignKeyViolation string
	if err := state.db.QueryRowContext(ctx, "SELECT 'violation' FROM pragma_foreign_key_check LIMIT 1").Scan(&foreignKeyViolation); err != sql.ErrNoRows {
		t.Fatalf("foreign key check = %q, %v", foreignKeyViolation, err)
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE usage_observations SET snapshot_id = 'missing-snapshot' WHERE observation_id = 'observation-1'`); err == nil {
		t.Fatal("migrated observation accepted a missing snapshot reference")
	}
	if _, err := state.db.ExecContext(ctx, `INSERT INTO usage_snapshots (snapshot_id, profile_id, source, source_version, captured_at, trigger_reason) VALUES ('snapshot-2', 'profile-1', 'codex_app_server', '1.0', '2026-09-18T12:05:00Z', 'periodic_active')`); err != nil {
		t.Fatalf("periodic trigger rejected after migration: %v", err)
	}
}

func TestCollectionScheduleTargetsPersistStateAndDeriveManagedLaunchActivity(t *testing.T) {
	state, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	home := addReadyProfile(t, state, "profile-1", "Work")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	plan, err := state.PrepareLaunch(context.Background(), launch.PrepareRequest{Alias: "Work", Executable: filepath.Join(home, "codex"), WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkManagedLaunchStarted(context.Background(), plan.LeaseID, 1234); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	wantState := usage.ScheduleState{LastAttemptAt: when, NextAttemptAt: when.Add(5 * time.Minute), ConsecutiveFailures: 2, LastOutcome: usage.ScheduleOutcomeFailed}
	if err := state.SaveCollectionScheduleState(context.Background(), "profile-1", wantState); err != nil {
		t.Fatal(err)
	}
	targets, err := state.CollectionScheduleTargets(context.Background())
	if err != nil || len(targets) != 1 {
		t.Fatalf("CollectionScheduleTargets() = %#v, %v", targets, err)
	}
	if !targets[0].Active || targets[0].Profile.ID != "profile-1" || targets[0].State != wantState {
		t.Fatalf("target = %#v", targets[0])
	}
}

func TestSchedulerCollectsThroughRealUsageServiceAndSQLiteStore(t *testing.T) {
	state, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	home := addReadyProfile(t, state, "profile-1", "Work")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	metric := usage.Registry()[0]
	availability := make([]usage.MetricAvailability, 0, len(usage.Registry()))
	for _, registered := range usage.Registry() {
		state := usage.AvailabilityUnsupported
		if registered.Key == metric.Key {
			state = usage.AvailabilityAvailable
		}
		availability = append(availability, usage.MetricAvailability{MetricKey: registered.Key, State: state, CheckedAt: now, Provenance: usage.ProvenanceProvider})
	}
	collector := &schedulerCollector{snapshot: usage.Snapshot{
		Source: usage.SourceCodexAppServer, SourceVersion: "fixture", CapturedAt: now,
		Observations: []usage.Observation{{Metric: metric, Value: 25, ObservedAt: now, CapturedAt: now, Provenance: usage.ProvenanceProvider, Availability: usage.AvailabilityAvailable}},
		Availability: availability,
	}}
	workflow, err := usage.NewService(state, collector, schedulerClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	scheduler, err := usage.NewScheduler(state, schedulerUsageService{workflow: workflow}, schedulerClock{now: now}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scheduler.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Collected != 1 || result.Failed != 0 || collector.calls != 1 {
		t.Fatalf("collection result = %#v, calls = %d", result, collector.calls)
	}
	latest, err := state.LatestUsageSnapshot(context.Background(), usage.ProfileTarget{ID: "profile-1", Alias: "Work"})
	if err != nil {
		t.Fatal(err)
	}
	if latest.TriggerReason != usage.TriggerPeriodicIdle || latest.SourceVersion != "fixture" {
		t.Fatalf("persisted snapshot = %#v", latest)
	}
}

type schedulerClock struct{ now time.Time }

func (clock schedulerClock) Now() time.Time { return clock.now }

type schedulerCollector struct {
	snapshot usage.Snapshot
	calls    int
}

func (collector *schedulerCollector) Collect(context.Context, usage.CollectionRequest) (usage.Snapshot, error) {
	collector.calls++
	return collector.snapshot, nil
}

type schedulerUsageService struct{ workflow *usage.Service }

func (service schedulerUsageService) Refresh(ctx context.Context, alias, trigger string) (usage.Snapshot, error) {
	return service.workflow.Refresh(ctx, alias, "/fixture/codex", "fixture", trigger)
}
