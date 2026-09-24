package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/usage"
)

func TestUnassignedMigrationPreservesLegacyAttributionWithoutDuplicatingSessions(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations()[:27] {
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := migration.apply(ctx, tx); err != nil {
			_ = tx.Rollback()
			t.Fatalf("migration %d: %v", migration.version, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"work", "personal"} {
		if _, err := database.ExecContext(ctx, `INSERT INTO identity_profiles (profile_id, display_name, status, created_at, updated_at) VALUES (?, ?, 'ready', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z')`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct{ id, profile, session string }{
		{"old-1", "work", "session-shared"}, {"old-2", "personal", "session-shared"}, {"old-3", "work", "session-unique"},
	} {
		if _, err := database.ExecContext(ctx, `INSERT INTO observed_sessions (observed_session_id, profile_id, source, source_session_id, source_version, started_at, last_observed_at, correlation_state) VALUES (?, ?, 'local_metadata', ?, 'state_5', '2026-09-01T00:00:00Z', '2026-09-01T00:01:00Z', 'uncorrelated')`, row.id, row.profile, row.session); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations()[27].apply(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := database.QueryContext(ctx, `SELECT source_session_id, COALESCE(profile_id, ''), attribution_provenance FROM observed_sessions ORDER BY source_session_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := []string{}
	for rows.Next() {
		var session, profile, provenance string
		if err := rows.Scan(&session, &profile, &provenance); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s:%s:%s", session, profile, provenance))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"session-shared::unassigned", "session-unique:work:legacy_profile_observation"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("migrated = %v, want %v", got, want)
	}
}

func TestObservedSessionsAndManagedLaunchesRemainDistinctAcrossRestart(t *testing.T) {
	stateStore, secureVault, _, home := readyLaunchStore(t)
	path := stateStore.path
	project := activity.ProjectRecord{ID: "project-1", Alias: "Folio", Basename: "repository", CanonicalPath: filepath.Join(home, "repository"), CreatedAt: time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC)}
	if err := stateStore.SaveProjectRecord(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	const sourceSessionID = "018f4f70-6f77-7c3f-9b77-93aa087dfc4d"
	plan, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{
		Alias: "Work", Executable: filepath.Join(home, "codex"), WorkingDirectory: project.CanonicalPath,
		Arguments: []string{"resume", sourceSessionID}, ProjectID: project.ID, ExpectedSessionID: sourceSessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), plan.LeaseID, 4321); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchExited(context.Background(), plan.LeaseID, 0); err != nil {
		t.Fatal(err)
	}
	tokens := int64(42)
	if err := stateStore.SaveObservedSessions(context.Background(), []activity.ObservedSessionRecord{{
		SourceSessionID: sourceSessionID, ProfileID: "profile-1", ProfileAlias: "Work", ProjectID: project.ID,
		ProjectAlias: project.Alias, ProjectBasename: project.Basename, Source: activity.SourceLocalMetadata,
		SourceVersion: "0.150.1", StartedAt: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
		LastObservedAt: time.Date(2026, 9, 2, 12, 5, 0, 0, time.UTC), Model: "gpt-5", TokensUsed: &tokens,
	}}); err != nil {
		t.Fatalf("SaveObservedSessions() error = %v", err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenWithOptions(Options{Path: path, Clock: launchClock{now: time.Date(2026, 9, 2, 13, 0, 0, 0, time.UTC)}, Vault: secureVault})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	timeline, err := reopened.ListActivity(context.Background(), activity.Filters{ProfileAlias: "work", ProjectID: project.ID})
	if err != nil {
		t.Fatalf("ListActivity() error = %v", err)
	}
	if len(timeline) != 2 || timeline[0].RecordType != activity.RecordTypeObservedSession || timeline[1].RecordType != activity.RecordTypeManagedLaunch {
		t.Fatalf("timeline = %#v", timeline)
	}
	for _, record := range timeline {
		if record.ID == "" || record.ProfileAlias != "Work" || record.ProjectAlias != "Folio" || record.ProjectBasename != "repository" {
			t.Fatalf("unsafe or incomplete record = %#v", record)
		}
		if record.Correlation.State != activity.CorrelationCorrelated || record.Correlation.EvidenceType != "explicit" || record.Correlation.Confidence != "high" {
			t.Fatalf("correlation = %#v", record.Correlation)
		}
	}
	if timeline[0].ID == timeline[1].ID || timeline[0].SourceSessionID != sourceSessionID || timeline[1].Lifecycle != string(launch.StateExited) || timeline[1].ExitStatus == nil || *timeline[1].ExitStatus != 0 {
		t.Fatalf("distinct records = %#v", timeline)
	}
}

func TestUnassignedSessionDeduplicatesAcrossRegistrations(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	addReadyProfile(t, stateStore, "profile-1", "Work")
	addReadyProfile(t, stateStore, "profile-2", "Personal")
	start := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	first := activity.ObservedSessionRecord{SourceSessionID: "018f4f70-6f77-7c3f-9b77-93aa087dfc4d", Source: activity.SourceLocalMetadata, SourceVersion: "state_5", StartedAt: start, LastObservedAt: start}
	if err := stateStore.SaveObservedSessions(context.Background(), []activity.ObservedSessionRecord{first}); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ProfileID, second.ProfileAlias = "profile-2", "Personal"
	second.LastObservedAt = start.Add(time.Minute)
	if err := stateStore.SaveObservedSessions(context.Background(), []activity.ObservedSessionRecord{second}); err != nil {
		t.Fatal(err)
	}
	other := first
	other.SourceSessionID = "018f4f70-6f77-7c3f-9b77-93aa087dfc4e"
	if err := stateStore.SaveObservedSessions(context.Background(), []activity.ObservedSessionRecord{other}); err != nil {
		t.Fatal(err)
	}
	all, err := stateStore.ListActivity(context.Background(), activity.Filters{})
	if err != nil || len(all) != 2 {
		t.Fatalf("overall activity = %#v, %v", all, err)
	}
	for _, record := range all {
		if record.ProfileID != "" || record.ProfileAlias != "Unassigned History" || record.AttributionProvenance != "unassigned" {
			t.Fatalf("unassigned record = %#v", record)
		}
	}
	personal, err := stateStore.ListActivity(context.Background(), activity.Filters{ProfileAlias: "Personal"})
	if err != nil || len(personal) != 0 {
		t.Fatalf("profile activity = %#v, %v", personal, err)
	}
	scope := usage.HistoryScope{ProfileID: "*", ProjectID: "*", From: "all", To: "all", Classes: []string{"observed_sessions"}}
	preview, err := stateStore.PurgeAnalytics(context.Background(), scope, "")
	var observedCount int64
	for _, item := range preview.Counts {
		if item.RecordClass == "observed_sessions" {
			observedCount = item.Count
		}
	}
	if err != nil || observedCount != 2 {
		t.Fatalf("unassigned purge preview = %#v, %v", preview, err)
	}
	if _, err := stateStore.PurgeAnalytics(context.Background(), scope, preview.Confirmation); err != nil {
		t.Fatal(err)
	}
	remaining, err := stateStore.ListActivity(context.Background(), activity.Filters{})
	if err != nil || len(remaining) != 0 {
		t.Fatalf("remaining unassigned records = %#v, %v", remaining, err)
	}
}

func TestHistoricalAssignmentIsAtomicAndSurvivesReimport(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	addReadyProfile(t, stateStore, "profile-1", "Work")
	addReadyProfile(t, stateStore, "profile-2", "Personal")
	ctx := context.Background()
	at := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	first := activity.ObservedSessionRecord{SourceSessionID: "source-1", Source: activity.SourceLocalMetadata, SourceVersion: "state_5", StartedAt: at, LastObservedAt: at}
	second := first
	second.SourceSessionID = "source-2"
	if err := stateStore.SaveObservedSessions(ctx, []activity.ObservedSessionRecord{first, second}); err != nil {
		t.Fatal(err)
	}
	all, err := stateStore.ListActivity(ctx, activity.Filters{})
	if err != nil || len(all) != 2 {
		t.Fatalf("initial history = %#v, %v", all, err)
	}
	ids := []string{all[0].ID, all[1].ID}
	if err := stateStore.AssignSessions(ctx, []string{ids[0], "missing"}, "profile-1"); err == nil {
		t.Fatal("invalid bulk assignment succeeded")
	}
	work, err := stateStore.ListActivity(ctx, activity.Filters{ProfileAlias: "Work"})
	if err != nil || len(work) != 0 {
		t.Fatalf("partial assignment = %#v, %v", work, err)
	}
	if err := stateStore.AssignSessions(ctx, ids, "profile-1"); err != nil {
		t.Fatal(err)
	}
	work, err = stateStore.ListActivity(ctx, activity.Filters{ProfileAlias: "Work"})
	if err != nil || len(work) != 2 {
		t.Fatalf("assigned total = %#v, %v", work, err)
	}
	for _, row := range work {
		if row.AttributionProvenance != "user_assigned" || row.OriginalProfileID != "" || row.OriginalAttributionProvenance != "unassigned" {
			t.Fatalf("attribution changed: %#v", row)
		}
	}
	if err := stateStore.AssignSessions(ctx, []string{ids[0]}, "profile-2"); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.AssignSessions(ctx, []string{ids[1]}, ""); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.SaveObservedSessions(ctx, []activity.ObservedSessionRecord{first, second}); err != nil {
		t.Fatal(err)
	}
	all, err = stateStore.ListActivity(ctx, activity.Filters{})
	if err != nil || len(all) != 2 {
		t.Fatalf("reimported history = %#v, %v", all, err)
	}
	personal, err := stateStore.ListActivity(ctx, activity.Filters{ProfileAlias: "Personal"})
	if err != nil || len(personal) != 1 || personal[0].ID != ids[0] {
		t.Fatalf("corrected total = %#v, %v", personal, err)
	}
	work, err = stateStore.ListActivity(ctx, activity.Filters{ProfileAlias: "Work"})
	if err != nil || len(work) != 0 {
		t.Fatalf("stale work total = %#v, %v", work, err)
	}
	for _, row := range all {
		if row.ID == ids[1] && (row.ProfileID != "" || row.AttributionProvenance != "user_assigned") {
			t.Fatalf("returned history = %#v", row)
		}
	}
}

func TestObservedSessionDoesNotGuessAcrossConflictingProfiles(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	addReadyProfile(t, stateStore, "profile-1", "Work")
	addReadyProfile(t, stateStore, "profile-2", "Personal")
	const sourceSessionID = "018f4f70-6f77-7c3f-9b77-93aa087dfc4d"
	if _, err := stateStore.db.Exec(`INSERT INTO managed_launches (managed_launch_id, profile_id, lease_id, project_identity_id, state, started_at, expected_session_id) VALUES ('launch-1', 'profile-2', 'lease-1', NULL, 'exited', '2026-09-02T12:00:00Z', ?)`, sourceSessionID); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.SaveObservedSessions(context.Background(), []activity.ObservedSessionRecord{{SourceSessionID: sourceSessionID, ProfileID: "profile-1", ProfileAlias: "Work", Source: activity.SourceLocalMetadata, SourceVersion: "0.150.1", StartedAt: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC), LastObservedAt: time.Date(2026, 9, 2, 12, 1, 0, 0, time.UTC)}}); err != nil {
		t.Fatal(err)
	}
	timeline, err := stateStore.ListActivity(context.Background(), activity.Filters{ProfileAlias: "Work"})
	if err != nil || len(timeline) != 1 || timeline[0].Correlation.State != activity.CorrelationContradictory || timeline[0].Correlation.ManagedLaunchID != "" {
		t.Fatalf("timeline = %#v, error = %v", timeline, err)
	}
}

func TestObservedSessionDoesNotGuessAcrossRepeatedManagedLaunches(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	addReadyProfile(t, stateStore, "profile-1", "Work")
	const sourceSessionID = "018f4f70-6f77-7c3f-9b77-93aa087dfc4d"
	for _, values := range [][2]string{{"launch-1", "lease-1"}, {"launch-2", "lease-2"}} {
		if _, err := stateStore.db.Exec(`INSERT INTO managed_launches (managed_launch_id, profile_id, lease_id, project_identity_id, state, started_at, expected_session_id) VALUES (?, 'profile-1', ?, NULL, 'exited', '2026-09-02T12:00:00Z', ?)`, values[0], values[1], sourceSessionID); err != nil {
			t.Fatal(err)
		}
	}
	if err := stateStore.SaveObservedSessions(context.Background(), []activity.ObservedSessionRecord{{SourceSessionID: sourceSessionID, ProfileID: "profile-1", ProfileAlias: "Work", Source: activity.SourceLocalMetadata, SourceVersion: "0.150.1", StartedAt: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC), LastObservedAt: time.Date(2026, 9, 2, 12, 1, 0, 0, time.UTC)}}); err != nil {
		t.Fatal(err)
	}
	timeline, err := stateStore.ListActivity(context.Background(), activity.Filters{ProfileAlias: "Work"})
	if err != nil || len(timeline) != 3 || timeline[0].Correlation.State != activity.CorrelationAmbiguous || timeline[0].Correlation.ManagedLaunchID != "" {
		t.Fatalf("timeline = %#v, error = %v", timeline, err)
	}
}

func TestObservedSessionRefreshPreservesNewerFactsAndResolvedProject(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	addReadyProfile(t, stateStore, "profile-1", "Work")
	project := activity.ProjectRecord{ID: "project-1", Alias: "Folio", Basename: "folio", CanonicalPath: filepath.Join(t.TempDir(), "folio"), CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	if err := stateStore.SaveProjectRecord(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	newerTokens, staleTokens := int64(50), int64(10)
	newer := activity.ObservedSessionRecord{SourceSessionID: "session-1", ProfileID: "profile-1", ProfileAlias: "Work", ProjectID: project.ID, Source: activity.SourceLocalMetadata, SourceVersion: "state_5", StartedAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), LastObservedAt: time.Date(2026, 9, 1, 10, 5, 0, 0, time.UTC), Model: "gpt-5", TokensUsed: &newerTokens}
	if err := stateStore.SaveObservedSessions(context.Background(), []activity.ObservedSessionRecord{newer}); err != nil {
		t.Fatal(err)
	}
	stale := newer
	stale.ProjectID, stale.LastObservedAt, stale.Model, stale.TokensUsed = "", newer.LastObservedAt.Add(-time.Minute), "gpt-4", &staleTokens
	if err := stateStore.SaveObservedSessions(context.Background(), []activity.ObservedSessionRecord{stale}); err != nil {
		t.Fatal(err)
	}
	records, err := stateStore.ListActivity(context.Background(), activity.Filters{ProfileAlias: "Work"})
	if err != nil || len(records) != 1 || records[0].ProjectID != project.ID || records[0].LastObservedAt != newer.LastObservedAt || records[0].Model != "gpt-5" || records[0].TokensUsed == nil || *records[0].TokensUsed != newerTokens {
		t.Fatalf("records = %#v/%v", records, err)
	}
}

func TestObservedSessionIgnoresUnstartedCorrelationAndExpiredRows(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	addReadyProfile(t, stateStore, "profile-1", "Work")
	if _, err := stateStore.SetAnalyticsRetention(context.Background(), "30"); err != nil {
		t.Fatal(err)
	}
	if _, err := stateStore.db.Exec(`INSERT INTO managed_launches (managed_launch_id, profile_id, lease_id, state, started_at, expected_session_id) VALUES ('pending-1', 'profile-1', 'lease-1', 'pending', '2026-09-01T00:00:00Z', 'recent')`); err != nil {
		t.Fatal(err)
	}
	records := []activity.ObservedSessionRecord{
		{SourceSessionID: "recent", ProfileID: "profile-1", ProfileAlias: "Work", Source: activity.SourceLocalMetadata, SourceVersion: "state_5", StartedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), LastObservedAt: time.Date(2026, 9, 1, 0, 1, 0, 0, time.UTC)},
		{SourceSessionID: "expired", ProfileID: "profile-1", ProfileAlias: "Work", Source: activity.SourceLocalMetadata, SourceVersion: "state_5", StartedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), LastObservedAt: time.Date(2026, 7, 1, 0, 1, 0, 0, time.UTC)},
	}
	if err := stateStore.SaveObservedSessions(context.Background(), records); err != nil {
		t.Fatal(err)
	}
	timeline, err := stateStore.ListActivity(context.Background(), activity.Filters{ProfileAlias: "Work"})
	if err != nil || len(timeline) != 2 || timeline[0].SourceSessionID != "recent" || timeline[0].Correlation.State != activity.CorrelationUncorrelated || timeline[1].RecordType != activity.RecordTypeManagedLaunch {
		t.Fatalf("timeline = %#v/%v", timeline, err)
	}
}
