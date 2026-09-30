package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/usage"
)

func TestUsageEligibilityRequiresAnExistingHome(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stateStore.Close() }()
	missing := addReadyProfile(t, stateStore, "profile-1", "Missing")
	existing := addReadyProfile(t, stateStore, "profile-2", "Existing")
	if err := os.MkdirAll(existing, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("missing-home fixture: %v", err)
	}
	profiles, err := stateStore.ListUsageProfiles(context.Background())
	if err != nil || len(profiles) != 2 || !profiles[0].Eligible || profiles[1].Eligible {
		t.Fatalf("existing/missing home eligibility = %#v/%v", profiles, err)
	}
	if err := os.Rename(existing, filepath.Join(filepath.Dir(existing), "moved-home")); err != nil {
		t.Fatal(err)
	}
	profiles, err = stateStore.ListUsageProfiles(context.Background())
	if err != nil || profiles[0].Eligible {
		t.Fatalf("moved home remained eligible: %#v/%v", profiles, err)
	}
}

func TestUsageSnapshotPersistsNoActivityWithoutObservation(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stateStore.Close() }()
	addReadyProfile(t, stateStore, "profile-1", "Work")
	target, err := stateStore.ResolveUsageProfile(context.Background(), "Work")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	snapshot := usage.NewUnavailableSnapshot("0.153.4", at, usage.AvailabilityUnsupported, usage.ReasonUnsupported)
	snapshot.Status = usage.AvailabilityPartial
	snapshot.TriggerReason = usage.TriggerExplicitRefresh
	snapshot.Availability[2].State = usage.AvailabilityNoActivity
	snapshot.Availability[2].Reason = usage.ReasonNoActivity
	if _, err := stateStore.SaveUsageSnapshot(context.Background(), target, snapshot); err != nil {
		t.Fatalf("SaveUsageSnapshot() error = %v: %v", err, errors.Unwrap(err))
	}
	loaded, err := stateStore.LatestUsageSnapshot(context.Background(), target)
	if err != nil {
		t.Fatalf("LatestUsageSnapshot() error = %v", err)
	}
	for _, availability := range loaded.Availability {
		if availability.MetricKey == "codex.local.tokens_used" {
			if availability.State != usage.AvailabilityNoActivity || availability.Reason != usage.ReasonNoActivity || len(loaded.Observations) != 0 {
				t.Fatalf("loaded empty history = %#v", loaded)
			}
			return
		}
	}
	t.Fatal("local token availability missing")
}

func TestUsageSnapshotPersistsAtomicallyWithoutChangingSelection(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stateStore.Close() }()
	addReadyProfile(t, stateStore, "profile-1", "Personal")
	home := addReadyProfile(t, stateStore, "profile-2", "Work")
	if _, err := stateStore.SelectProfile(context.Background(), "Personal"); err != nil {
		t.Fatal(err)
	}

	target, err := stateStore.ResolveUsageProfile(context.Background(), "work")
	if err != nil {
		t.Fatalf("ResolveUsageProfile() error = %v", err)
	}
	if target.ID != "profile-2" || target.Alias != "Work" || target.IdentityHome != home {
		t.Fatalf("target = %#v", target)
	}
	capturedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	windowStart, windowEnd := capturedAt, capturedAt.Add(5*time.Hour)
	metric := usage.Registry()[0]
	snapshot, err := stateStore.SaveUsageSnapshot(context.Background(), target, usage.Snapshot{
		Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: capturedAt, Status: usage.AvailabilityPartial, TriggerReason: usage.TriggerDashboardOpen,
		Observations: []usage.Observation{{Metric: metric, Value: 25, ObservedAt: capturedAt, CapturedAt: capturedAt, WindowStart: &windowStart, WindowEnd: &windowEnd, WindowTimezone: "UTC", Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable}},
		Availability: completeUsageAvailability(capturedAt, []usage.MetricAvailability{
			{MetricKey: metric.Key, State: usage.AvailabilityAvailable, CheckedAt: capturedAt, Provenance: usage.ProvenanceProvider},
			{MetricKey: usage.Registry()[1].Key, State: usage.AvailabilityUnsupported, CheckedAt: capturedAt, Provenance: usage.ProvenanceProvider},
		}),
	})
	if err != nil {
		t.Fatalf("SaveUsageSnapshot() error = %v", err)
	}
	if snapshot.ID == "" || snapshot.ProfileID != "profile-2" || snapshot.Alias != "Work" || snapshot.TriggerReason != usage.TriggerDashboardOpen || snapshot.Observations[0].ID == "" || snapshot.Availability[0].ID == "" {
		t.Fatalf("saved snapshot = %#v", snapshot)
	}

	var snapshots, observations, availability, provenance int
	for table, destination := range map[string]*int{"usage_snapshots": &snapshots, "usage_observations": &observations, "metric_availability": &availability, "metric_provenance": &provenance} {
		if err := stateStore.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	if snapshots != 1 || observations != 1 || availability != len(usage.Registry()) || provenance != 2 {
		t.Fatalf("persisted counts = snapshots:%d observations:%d availability:%d provenance:%d", snapshots, observations, availability, provenance)
	}
	profiles, err := stateStore.ListProfiles(context.Background())
	if err != nil || len(profiles) != 2 || !profiles[0].Selected || profiles[0].ID != "profile-1" {
		t.Fatalf("profiles after refresh = %#v/%v, want profile-1 selected", profiles, err)
	}
}

func TestLastUsageObservationsSurviveFailedRefreshAndRestart(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stateStore.Close() }()
	addReadyProfile(t, stateStore, "profile-1", "Work")
	target, err := stateStore.ResolveUsageProfile(context.Background(), "Work")
	if err != nil {
		t.Fatal(err)
	}
	capturedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	windowStart, windowEnd := capturedAt.Add(-5*time.Hour), capturedAt.Add(time.Hour)
	metric := usage.Registry()[0]
	saved, err := stateStore.SaveUsageSnapshot(context.Background(), target, usage.Snapshot{
		Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: capturedAt, Status: usage.AvailabilityPartial, TriggerReason: usage.TriggerExplicitRefresh,
		Observations: []usage.Observation{{Metric: metric, Value: 0, ObservedAt: capturedAt, CapturedAt: capturedAt, WindowStart: &windowStart, WindowEnd: &windowEnd, WindowTimezone: "Asia/Kolkata", Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable}},
		Availability: completeUsageAvailability(capturedAt, []usage.MetricAvailability{
			{MetricKey: metric.Key, State: usage.AvailabilityAvailable, CheckedAt: capturedAt, Provenance: usage.ProvenanceProvider},
			{MetricKey: usage.Registry()[1].Key, State: usage.AvailabilityUnsupported, Reason: usage.ReasonUnsupported, CheckedAt: capturedAt, Provenance: usage.ProvenanceProvider},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	failure := usage.NewUnavailableSnapshot("0.153.4", capturedAt.Add(15*time.Minute), usage.AvailabilityTemporarilyUnavailable, usage.ReasonCollectionFailed)
	failure.TriggerReason = usage.TriggerPostExit
	_, err = stateStore.SaveUsageSnapshot(context.Background(), target, failure)
	if err != nil {
		t.Fatal(err)
	}
	databasePath, secureVault := stateStore.path, stateStore.vault
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}
	stateStore, err = OpenWithVault(databasePath, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := stateStore.LatestUsageSnapshot(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	observations := latest.Observations
	if len(observations) != 1 || observations[0].ID != saved.Observations[0].ID || observations[0].Value != 0 || observations[0].WindowTimezone != "Asia/Kolkata" || observations[0].SourceVersion != "0.153.4" || observations[0].WindowStart == nil || observations[0].WindowEnd == nil {
		t.Fatalf("last observations = %#v", observations)
	}
	if latest.Status != usage.AvailabilityTemporarilyUnavailable || latest.TriggerReason != usage.TriggerPostExit || latest.Availability[0].Reason != usage.ReasonCollectionFailed || latest.Availability[0].CheckedAt != capturedAt.Add(15*time.Minute) {
		t.Fatalf("latest availability = %#v", latest)
	}
}

func TestContradictoryUsageSourcesRetainProvenanceAcrossRestart(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	databasePath, secureVault := stateStore.path, stateStore.vault
	addReadyProfile(t, stateStore, "profile-1", "Work")
	target, err := stateStore.ResolveUsageProfile(context.Background(), "Work")
	if err != nil {
		t.Fatal(err)
	}
	capturedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	metric := usage.Registry()[0]
	snapshot := usage.Snapshot{
		Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: capturedAt, Status: usage.AvailabilityContradictory, TriggerReason: usage.TriggerDashboardRefresh,
		Observations: []usage.Observation{
			{Metric: metric, Value: 0, ObservedAt: capturedAt, CapturedAt: capturedAt, Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityContradictory},
			{Metric: metric, Value: 10, ObservedAt: capturedAt, CapturedAt: capturedAt, Source: usage.SourceCodexAppServer, SourceVersion: "0.153.3", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityContradictory},
		},
		Availability: completeUsageAvailability(capturedAt, []usage.MetricAvailability{
			{MetricKey: metric.Key, State: usage.AvailabilityContradictory, Reason: usage.ReasonContradictory, CheckedAt: capturedAt, Provenance: usage.ProvenanceProvider},
			{MetricKey: usage.Registry()[1].Key, State: usage.AvailabilityUnsupported, Reason: usage.ReasonUnsupported, CheckedAt: capturedAt, Provenance: usage.ProvenanceProvider},
		}),
	}
	if _, err := stateStore.SaveUsageSnapshot(context.Background(), target, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}
	stateStore, err = OpenWithVault(databasePath, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stateStore.Close() }()
	observations, err := stateStore.LastUsageObservations(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 2 || observations[0].SourceVersion == observations[1].SourceVersion || observations[0].Value == observations[1].Value || observations[0].Availability != usage.AvailabilityContradictory || observations[1].Availability != usage.AvailabilityContradictory {
		t.Fatalf("contradictory observations = %#v", observations)
	}
}

func TestUsageSnapshotPersistsEveryProvenanceLabel(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stateStore.Close() }()
	addReadyProfile(t, stateStore, "profile-1", "Work")
	target, err := stateStore.ResolveUsageProfile(context.Background(), "Work")
	if err != nil {
		t.Fatal(err)
	}
	capturedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	metric := usage.Registry()[0]
	observations := []usage.Observation{
		{Metric: metric, Value: 10, ObservedAt: capturedAt, CapturedAt: capturedAt, Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable},
		{Metric: metric, Value: 10, ObservedAt: capturedAt, CapturedAt: capturedAt, Source: usage.SourceLocalMetadata, SourceVersion: "v1", Provenance: usage.ProvenanceLocal, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable},
		{Metric: metric, Value: 10, ObservedAt: capturedAt, CapturedAt: capturedAt, Source: usage.SourceDerived, SourceVersion: "v1", Provenance: usage.ProvenanceEstimated, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable, Uncertainty: "provider updates may lag"},
		{Metric: metric, Value: 10, ObservedAt: capturedAt, CapturedAt: capturedAt, Source: usage.SourceLocalMetadata, SourceVersion: "v1", Provenance: usage.ProvenanceObserved, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable},
	}
	snapshot := usage.Snapshot{
		Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: capturedAt, Status: usage.AvailabilityPartial, TriggerReason: usage.TriggerExplicitRefresh,
		Observations: observations,
		Availability: completeUsageAvailability(capturedAt, []usage.MetricAvailability{
			{MetricKey: metric.Key, State: usage.AvailabilityAvailable, CheckedAt: capturedAt, Provenance: usage.ProvenanceProvider},
			{MetricKey: usage.Registry()[1].Key, State: usage.AvailabilityUnsupported, CheckedAt: capturedAt, Provenance: usage.ProvenanceProvider},
		}),
	}
	if _, err := stateStore.SaveUsageSnapshot(context.Background(), target, snapshot); err != nil {
		t.Fatal(err)
	}
	stored, err := stateStore.LastUsageObservations(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	labels := make(map[string]bool, len(stored))
	for _, observation := range stored {
		labels[observation.Provenance] = true
	}
	for _, want := range []string{usage.ProvenanceProvider, usage.ProvenanceLocal, usage.ProvenanceEstimated, usage.ProvenanceObserved} {
		if !labels[want] {
			t.Fatalf("stored provenance = %v, missing %q", labels, want)
		}
	}
	latest, err := stateStore.LatestUsageSnapshot(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	availabilityLabels := map[string]string{}
	for _, item := range latest.Availability {
		availabilityLabels[item.MetricKey] = item.Provenance
	}
	if availabilityLabels[usage.Registry()[2].Key] != usage.ProvenanceLocal || availabilityLabels[usage.Registry()[3].Key] != usage.ProvenanceLocal {
		t.Fatalf("availability provenance = %#v", availabilityLabels)
	}

	snapshot.Observations[2].Uncertainty = ""
	if _, err := stateStore.SaveUsageSnapshot(context.Background(), target, snapshot); !errors.Is(err, usage.ErrInvalid) {
		t.Fatalf("SaveUsageSnapshot() estimated without uncertainty error = %v", err)
	}
}

func TestLastUsageObservationsRetainsLatestEvidencePerSource(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	addReadyProfile(t, stateStore, "profile-1", "Work")
	target, err := stateStore.ResolveUsageProfile(context.Background(), "Work")
	if err != nil {
		t.Fatal(err)
	}
	capturedAt := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	metric := usage.Registry()[0]
	snapshot := usage.Snapshot{
		Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: capturedAt, Status: usage.AvailabilityAvailable, TriggerReason: usage.TriggerExplicitRefresh,
		Observations: []usage.Observation{
			{Metric: metric, Value: 25, ObservedAt: capturedAt, CapturedAt: capturedAt, Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable},
			{Metric: metric, Value: 20, ObservedAt: capturedAt.Add(-time.Minute), CapturedAt: capturedAt.Add(-time.Minute), Source: usage.SourceLocalMetadata, SourceVersion: "state_5", Provenance: usage.ProvenanceLocal, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable},
		},
		Availability: completeUsageAvailability(capturedAt, []usage.MetricAvailability{{MetricKey: metric.Key, State: usage.AvailabilityAvailable, CheckedAt: capturedAt, Provenance: usage.ProvenanceProvider}}),
	}
	if _, err := stateStore.SaveUsageSnapshot(context.Background(), target, snapshot); err != nil {
		t.Fatal(err)
	}
	observations, err := stateStore.LastUsageObservations(context.Background(), target)
	if err != nil || len(observations) != 2 {
		t.Fatalf("observations = %#v/%v", observations, err)
	}
}

func TestLatestUsageProjectsFreshCurrentWindowAndRetainsHistoricalEvidence(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	addReadyProfile(t, stateStore, "profile-1", "Work")
	target, err := stateStore.ResolveUsageProfile(context.Background(), "Work")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	metric := usage.Registry()[0]
	save := func(value float64, capturedAt, windowStart, windowEnd time.Time, sourceVersion string) {
		t.Helper()
		snapshot := usage.Snapshot{
			Source: usage.SourceCodexAppServer, SourceVersion: sourceVersion, CapturedAt: capturedAt,
			Status: usage.AvailabilityPartial, TriggerReason: usage.TriggerDashboardRefresh,
			Observations: []usage.Observation{{
				Metric: metric, Value: value, ObservedAt: capturedAt, CapturedAt: capturedAt,
				WindowStart: &windowStart, WindowEnd: &windowEnd, WindowTimezone: "UTC",
				Source: usage.SourceCodexAppServer, SourceVersion: sourceVersion,
				Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable,
			}},
			Availability: completeUsageAvailability(capturedAt, []usage.MetricAvailability{{MetricKey: metric.Key, State: usage.AvailabilityAvailable, CheckedAt: capturedAt, Provenance: usage.ProvenanceProvider}}),
		}
		if _, err := stateStore.SaveUsageSnapshot(context.Background(), target, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	save(75, now.Add(-7*24*time.Hour), now.Add(-14*24*time.Hour), now.Add(-7*24*time.Hour), "0.153.3")
	save(44, now, now.Add(-time.Hour), now.Add(4*time.Hour), "0.153.4")

	history, err := stateStore.LastUsageObservations(context.Background(), target)
	if err != nil || len(history) != 2 {
		t.Fatalf("retained observations = %#v/%v", history, err)
	}
	service, err := usage.NewService(stateStore, &schedulerCollector{}, schedulerClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	latest, err := service.Latest(context.Background(), "Work")
	if err != nil {
		t.Fatal(err)
	}
	primaryState := ""
	for _, availability := range latest.Availability {
		if availability.MetricKey == metric.Key {
			primaryState = availability.State
		}
	}
	if latest.Status != usage.AvailabilityPartial || primaryState != usage.AvailabilityAvailable || len(latest.Observations) != 1 || latest.Observations[0].Value != 44 {
		t.Fatalf("current projection = %#v", latest)
	}
	history, err = stateStore.LastUsageObservations(context.Background(), target)
	if err != nil || len(history) != 2 {
		t.Fatalf("projection changed retained observations = %#v/%v", history, err)
	}
}

func completeUsageAvailability(checkedAt time.Time, items []usage.MetricAvailability) []usage.MetricAvailability {
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		seen[item.MetricKey] = true
	}
	for _, metric := range usage.Registry() {
		if !seen[metric.Key] {
			items = append(items, usage.MetricAvailability{MetricKey: metric.Key, State: usage.AvailabilityUnsupported, Reason: usage.ReasonUnsupported, CheckedAt: checkedAt, Provenance: metric.SourceClass})
		}
	}
	return items
}

func TestUsageProfilesAndSnapshotScopeSurviveRestart(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	home := addReadyProfile(t, stateStore, "profile-1", "Personal")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	addReadyProfile(t, stateStore, "profile-2", "Work")
	if _, err := stateStore.SelectProfile(context.Background(), "Personal"); err != nil {
		t.Fatal(err)
	}
	if _, err := stateStore.SaveDocumentedMetadata(context.Background(), "profile-1", profile.DocumentedMetadata{LoginIdentity: "login-1", Workspace: "workspace-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := stateStore.db.ExecContext(context.Background(), `UPDATE identity_profiles SET status = 'needs_reauthentication' WHERE profile_id = 'profile-2'`); err != nil {
		t.Fatal(err)
	}

	target, err := stateStore.ResolveUsageProfile(context.Background(), "Personal")
	if err != nil || target.LoginIdentity != "login-1" || target.Workspace != "workspace-1" {
		t.Fatalf("resolved source scope/error = %#v/%v", target, err)
	}
	profiles, err := stateStore.ListUsageProfiles(context.Background())
	if err != nil || len(profiles) != 2 || !profiles[0].Selected || !profiles[0].Eligible || profiles[1].Eligible {
		t.Fatalf("usage profiles/error = %#v/%v", profiles, err)
	}
	reauthenticationTarget, err := stateStore.ResolveUsageProfile(context.Background(), "Personal")
	if err != nil {
		t.Fatal(err)
	}
	reauthentication := usage.NewUnavailableSnapshot("0.153.4", time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC), usage.AvailabilityReauthenticationRequired, usage.ReasonReauthentication)
	reauthentication.TriggerReason = usage.TriggerExplicitRefresh
	if _, err := stateStore.SaveUsageSnapshot(context.Background(), reauthenticationTarget, reauthentication); err != nil {
		t.Fatal(err)
	}
	profiles, err = stateStore.ListUsageProfiles(context.Background())
	if err != nil || profiles[0].Eligible {
		t.Fatalf("usage profiles after reauthentication/error = %#v/%v", profiles, err)
	}

	capturedAt := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	snapshot := usage.NewUnavailableSnapshot("0.153.4", capturedAt, usage.AvailabilityUnsupported, usage.ReasonUnsupported)
	snapshot.TriggerReason = usage.TriggerExplicitRefresh
	if _, err := stateStore.SaveUsageSnapshot(context.Background(), target, snapshot); err != nil {
		t.Fatal(err)
	}
	var loginCiphertext, workspaceCiphertext []byte
	if err := stateStore.db.QueryRowContext(context.Background(), `SELECT login_identity_ciphertext, workspace_ciphertext FROM usage_snapshots`).Scan(&loginCiphertext, &workspaceCiphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(loginCiphertext, []byte("login-1")) || bytes.Contains(workspaceCiphertext, []byte("workspace-1")) {
		t.Fatal("usage source scope was stored in plaintext")
	}
	path, secureVault := stateStore.path, stateStore.vault
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}
	stateStore, err = OpenWithVault(path, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stateStore.Close() }()
	stored, err := stateStore.LatestUsageSnapshot(context.Background(), target)
	if err != nil || stored.LoginIdentity != "login-1" || stored.Workspace != "workspace-1" {
		t.Fatalf("stored source scope/error = %#v/%v", stored, err)
	}
}

func TestRecentUsageSnapshotsBoundRawCapturesAndPreserveFailureGaps(t *testing.T) {
	state, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	addReadyProfile(t, state, "profile-recent", "Recent")
	ctx := context.Background()
	target, err := state.ResolveUsageProfile(ctx, "Recent")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 14; i++ {
		at := now.Add(time.Duration(i) * time.Minute)
		snapshot := usage.NewUnavailableSnapshot("0.153.4", at, usage.AvailabilityTemporarilyUnavailable, usage.ReasonCollectionFailed)
		snapshot.TriggerReason = usage.TriggerDashboardRefresh
		if i != 12 {
			metric := usage.Registry()[0]
			snapshot.Availability[0].State = usage.AvailabilityAvailable
			snapshot.Availability[0].Reason = ""
			snapshot.Observations = []usage.Observation{{Metric: metric, Value: float64(i), ObservedAt: at, CapturedAt: at, Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable}}
		}
		if _, err := state.SaveUsageSnapshot(ctx, target, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	recent, err := state.RecentUsageSnapshots(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 12 || len(recent[10].Observations) != 0 || recent[0].Observations[0].Value != 2 || recent[11].Observations[0].Value != 13 {
		t.Fatalf("recent trace did not retain twelve raw captures with a failure gap: %#v", recent)
	}
	if !recent[0].CapturedAt.Equal(now.Add(2 * time.Minute)) {
		t.Fatal("trace changed capture age")
	}
}

func TestLatestSuccessfulUsageRefreshLooksBeyondRecentTrace(t *testing.T) {
	state, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	addReadyProfile(t, state, "profile-success", "Success")
	ctx := context.Background()
	target, err := state.ResolveUsageProfile(ctx, "Success")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	success := usage.NewUnavailableSnapshot("0.153.4", now, usage.AvailabilityTemporarilyUnavailable, usage.ReasonCollectionFailed)
	success.TriggerReason = usage.TriggerDashboardRefresh
	success.Availability[0].State, success.Availability[0].Reason = usage.AvailabilityAvailable, ""
	success.Observations = []usage.Observation{{Metric: usage.Registry()[0], Value: 50, ObservedAt: now, CapturedAt: now, Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable}}
	if _, err := state.SaveUsageSnapshot(ctx, target, success); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 13; index++ {
		failure := usage.NewUnavailableSnapshot("0.153.4", now.Add(time.Duration(index)*time.Minute), usage.AvailabilityTemporarilyUnavailable, usage.ReasonCollectionFailed)
		failure.TriggerReason = usage.TriggerDashboardRefresh
		if _, err := state.SaveUsageSnapshot(ctx, target, failure); err != nil {
			t.Fatal(err)
		}
	}
	if recent, err := state.RecentUsageSnapshots(ctx, target); err != nil || len(recent) != 12 || !recent[0].CapturedAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("recent trace = %#v/%v", recent, err)
	}
	latest, err := state.LatestSuccessfulUsageRefresh(ctx, target)
	if err != nil || !latest.Equal(now) {
		t.Fatalf("LatestSuccessfulUsageRefresh() = %s/%v, want %s", latest, err, now)
	}

	emptyState, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer emptyState.Close()
	addReadyProfile(t, emptyState, "profile-empty", "Empty")
	emptyTarget, err := emptyState.ResolveUsageProfile(ctx, "Empty")
	if err != nil {
		t.Fatal(err)
	}
	if latest, err := emptyState.LatestSuccessfulUsageRefresh(ctx, emptyTarget); err != nil || !latest.IsZero() {
		t.Fatalf("empty LatestSuccessfulUsageRefresh() = %s/%v", latest, err)
	}
}

func TestUsageCaptureOrderingWithDifferentFractionalPrecision(t *testing.T) {
	state, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	addReadyProfile(t, state, "profile-fraction", "Fraction")
	ctx := context.Background()
	target, err := state.ResolveUsageProfile(ctx, "Fraction")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	for _, millis := range []int{0, 100, 110} {
		at := base.Add(time.Duration(millis) * time.Millisecond)
		snapshot := usage.NewUnavailableSnapshot("0.153.4", at, usage.AvailabilityTemporarilyUnavailable, usage.ReasonCollectionFailed)
		snapshot.TriggerReason = usage.TriggerDashboardRefresh
		snapshot.Availability[0].State = usage.AvailabilityAvailable
		snapshot.Availability[0].Reason = ""
		snapshot.Observations = []usage.Observation{{Metric: usage.Registry()[0], Value: float64(millis) / 10, ObservedAt: at, CapturedAt: at, Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable}}
		if _, err := state.SaveUsageSnapshot(ctx, target, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := state.LatestUsageSnapshot(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if !latest.CapturedAt.Equal(base.Add(110*time.Millisecond)) || len(latest.Observations) != 1 || latest.Observations[0].Value != 11 {
		t.Errorf("latest snapshot/observation did not select newest instant: %#v", latest)
	}
	recent, err := state.RecentUsageSnapshots(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 3 || !recent[0].CapturedAt.Equal(base) || !recent[2].CapturedAt.Equal(base.Add(110*time.Millisecond)) {
		t.Errorf("recent snapshots are not chronological: %#v", recent)
	}
}
