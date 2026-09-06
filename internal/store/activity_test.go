package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/launch"
)

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
