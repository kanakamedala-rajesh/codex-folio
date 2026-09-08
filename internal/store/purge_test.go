package store

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configpack"
	"venkatasudha.com/codex-folio/internal/usage"
)

func TestAnalyticsPurgePreviewConfirmationAndProfileIsolation(t *testing.T) {
	state, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	for _, item := range []struct{ id, alias string }{{"work", "Work"}, {"personal", "Personal"}} {
		addReadyProfile(t, state, item.id, item.alias)
		target, err := state.ResolveUsageProfile(ctx, item.alias)
		if err != nil {
			t.Fatal(err)
		}
		snapshot := usage.NewUnavailableSnapshot("0.153.4", time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC), usage.AvailabilityUnsupported, usage.ReasonUnsupported)
		snapshot.TriggerReason = usage.TriggerExplicitRefresh
		if _, err := state.SaveUsageSnapshot(ctx, target, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := state.SelectProfile(ctx, "Work"); err != nil {
		t.Fatal(err)
	}
	profiles, err := state.ListProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope := usage.HistoryScope{ProfileID: "work", ProjectID: "*", From: "2026-08-01T00:00:00Z", To: "2026-09-01T00:00:00Z", Classes: []string{"usage"}}
	preview, err := state.PurgeAnalytics(ctx, scope, "")
	if err != nil || preview.Applied || !preview.Executable {
		t.Fatalf("preview: %#v/%v", preview, err)
	}
	counts := map[string]int64{}
	for _, count := range preview.Counts {
		counts[count.RecordClass] = count.Count
	}
	if counts["usage_snapshots"] != 1 || counts["metric_availability"] != 4 || counts["metric_provenance"] != 2 {
		t.Fatalf("counts: %v", counts)
	}
	if _, err := state.PurgeAnalytics(ctx, scope, "yes"); apperrors.Code(err) != apperrors.AnalyticsConfirmationInvalid {
		t.Fatalf("confirmation: %v", err)
	}
	again, err := state.PurgeAnalytics(ctx, scope, "")
	if err != nil || !reflect.DeepEqual(preview, again) {
		t.Fatalf("dry run changed data: %#v/%v", again, err)
	}
	applied, err := state.PurgeAnalytics(ctx, scope, preview.Confirmation)
	if err != nil || !applied.Applied || !reflect.DeepEqual(applied.Counts, preview.Counts) {
		t.Fatalf("apply: %#v/%v", applied, err)
	}
	remaining, err := state.PurgeAnalytics(ctx, scope, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range remaining.Counts {
		if count.Count != 0 {
			t.Fatalf("remaining: %#v", remaining)
		}
	}
	scope.ProfileID = "personal"
	other, err := state.PurgeAnalytics(ctx, scope, "")
	if err != nil || !reflect.DeepEqual(preview.Counts, other.Counts) {
		t.Fatalf("other profile: %#v/%v", other, err)
	}
	after, err := state.ListProfiles(ctx)
	if err != nil || !reflect.DeepEqual(profiles, after) {
		t.Fatalf("profile lifecycle changed: %#v/%v", after, err)
	}
}

func TestPurgeProjectActivityRollbackRestartAndLifecycleIsolation(t *testing.T) {
	state, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	ctx := context.Background()
	home := addReadyProfile(t, state, "work", "Work")
	addReadyProfile(t, state, "quarantined", "Quarantined")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "fixture-auth-state")
	if err := os.WriteFile(marker, []byte("unchanged test fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := state.SelectProfile(ctx, "Work"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.Exec(`INSERT INTO profile_quarantine VALUES ('quarantined', 'quarantined', 0, '2026-08-01T00:00:00Z', '2026-09-10T00:00:00Z', '2026-08-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	pack, err := configpack.NewDraft("shared", "1", map[string]string{"config/base.toml": "model = \"gpt-5\"\n"})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.CreateConfigurationPack(ctx, pack); err != nil {
		t.Fatal(err)
	}
	pack, err = state.GetConfigurationPack(ctx, pack.ID, pack.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.Exec(`INSERT INTO settings (settings_id, diagnostics_retention_days, updated_at) VALUES (1, 17, '2026-08-01T00:00:00Z'); INSERT INTO retention_state VALUES (1, 400, 17, NULL, NULL, '2026-08-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	checkpoints := []Checkpoint{}
	for _, projectID := range []string{"project-a", "project-b"} {
		project := ProjectIdentity{ProjectIdentityID: projectID, ProjectAlias: projectID, CanonicalPath: filepath.Join(t.TempDir(), projectID), CreatedAt: at, UpdatedAt: at}
		if err := state.PutProjectIdentity(ctx, project); err != nil {
			t.Fatal(err)
		}
		goal := "sanitized checkpoint fixture"
		checkpoint := Checkpoint{CheckpointID: "checkpoint-" + projectID, ProjectIdentityID: projectID, Status: "approved", Goal: &goal, CreatedAt: at}
		if err := state.PutCheckpoint(ctx, checkpoint); err != nil {
			t.Fatal(err)
		}
		checkpoints = append(checkpoints, checkpoint)
		if _, err := state.db.Exec(`INSERT INTO managed_launches (managed_launch_id, profile_id, lease_id, project_identity_id, state, started_at, ended_at, expected_session_id) VALUES (?, 'work', ?, ?, 'exited', ?, ?, ?)`, "launch-"+projectID, "lease-"+projectID, projectID, formatStoredTime(at), formatStoredTime(at.Add(time.Minute)), "session-"+projectID); err != nil {
			t.Fatal(err)
		}
		if err := state.SaveObservedSessions(ctx, []activity.ObservedSessionRecord{{ProfileID: "work", ProfileAlias: "Work", SourceSessionID: "session-" + projectID, ProjectID: projectID, Source: usage.SourceLocalMetadata, SourceVersion: "v5", StartedAt: at, LastObservedAt: at.Add(time.Minute)}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := state.db.Exec(`INSERT INTO managed_launches (managed_launch_id, profile_id, lease_id, project_identity_id, state, started_at) VALUES ('running', 'work', 'live-lease', 'project-a', 'running', ?)`, formatStoredTime(at)); err != nil {
		t.Fatal(err)
	}
	target, err := state.ResolveUsageProfile(ctx, "Work")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := usage.NewUnavailableSnapshot("0.153.4", at, usage.AvailabilityUnsupported, usage.ReasonUnsupported)
	snapshot.TriggerReason = usage.TriggerExplicitRefresh
	if _, err := state.SaveUsageSnapshot(ctx, target, snapshot); err != nil {
		t.Fatal(err)
	}
	profiles, err := state.ListProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	timeline, err := state.ListActivity(ctx, activity.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	scope := usage.HistoryScope{ProfileID: "*", ProjectID: "project-a", From: "2026-08-01T00:00:00Z", To: "2026-08-02T00:00:00Z", Classes: []string{"usage", "aggregates", "observed_sessions", "managed_launches", "checkpoints"}}
	preview, err := state.PurgeAnalytics(ctx, scope, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range preview.Counts {
		want := int64(0)
		if count.RecordClass == "observed_sessions" || count.RecordClass == "managed_launches" || count.RecordClass == "checkpoints" || count.RecordClass == "correlation_evidence" {
			want = 1
		}
		if count.Count != want {
			t.Fatalf("%s: %d, want %d", count.RecordClass, count.Count, want)
		}
	}
	if _, err := state.db.Exec(`CREATE TRIGGER interrupt_purge BEFORE DELETE ON checkpoints BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := state.PurgeAnalytics(ctx, scope, preview.Confirmation); err == nil {
		t.Fatal("injected purge failure succeeded")
	}
	afterFailure, err := state.ListActivity(ctx, activity.Filters{})
	if err != nil || !reflect.DeepEqual(timeline, afterFailure) {
		t.Fatal("failed purge partially deleted activity")
	}
	if _, err := state.db.Exec(`DROP TRIGGER interrupt_purge`); err != nil {
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
	again, err := state.PurgeAnalytics(ctx, scope, "")
	if err != nil || !reflect.DeepEqual(preview, again) {
		t.Fatal("failed purge scope changed after restart")
	}
	launchScope := scope
	launchScope.Classes = []string{"managed_launches"}
	if _, err := state.PurgeAnalytics(ctx, launchScope, launchScope.Confirmation()); err != nil {
		t.Fatal(err)
	}
	afterLaunch, err := state.ListActivity(ctx, activity.Filters{ProjectID: "project-a"})
	if err != nil || len(afterLaunch) != 2 {
		t.Fatalf("launch-only purge: %d/%v", len(afterLaunch), err)
	}
	for _, record := range afterLaunch {
		if record.Correlation.State != activity.CorrelationUncorrelated {
			t.Fatal("orphaned correlation projection")
		}
	}
	if _, err := state.PurgeAnalytics(ctx, scope, preview.Confirmation); err != nil {
		t.Fatal(err)
	}
	remaining, err := state.ListActivity(ctx, activity.Filters{})
	if err != nil || len(remaining) != 3 {
		t.Fatalf("remaining timeline: %d/%v", len(remaining), err)
	}
	if _, err := state.LatestUsageSnapshot(ctx, target); err != nil {
		t.Fatal("project purge deleted unattributed provider usage")
	}
	if checkpoint, err := state.GetCheckpoint(ctx, checkpoints[1].CheckpointID); err != nil || !reflect.DeepEqual(checkpoint, checkpoints[1]) {
		t.Fatal("unrelated encrypted checkpoint or vault changed")
	}
	afterProfiles, err := state.ListProfiles(ctx)
	if err != nil || !reflect.DeepEqual(profiles, afterProfiles) {
		t.Fatal("profile registry or selection changed")
	}
	afterPack, err := state.GetConfigurationPack(ctx, pack.ID, pack.Version)
	if err != nil || !reflect.DeepEqual(pack, afterPack) {
		t.Fatal("configuration pack changed")
	}
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "unchanged test fixture" {
		t.Fatal("Identity Home authentication fixture changed")
	}
	var quarantine, diagnosticsDays, retentionDays int
	if err := state.db.QueryRow(`SELECT COUNT(*) FROM profile_quarantine WHERE profile_id = 'quarantined' AND state = 'quarantined'`).Scan(&quarantine); err != nil || quarantine != 1 {
		t.Fatal("quarantine changed")
	}
	if err := state.db.QueryRow(`SELECT diagnostics_retention_days FROM settings`).Scan(&diagnosticsDays); err != nil || diagnosticsDays != 17 {
		t.Fatal("diagnostics retention changed")
	}
	if err := state.db.QueryRow(`SELECT analytics_retention_days FROM retention_state`).Scan(&retentionDays); err != nil || retentionDays != 400 {
		t.Fatal("unrelated retention ledger changed")
	}
	rows, err := state.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("purge left orphaned references")
	}
}
