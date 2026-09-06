package store

import (
	"context"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/usage"
)

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
		Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: capturedAt,
		Observations: []usage.Observation{{Metric: metric, Value: 25, ObservedAt: capturedAt, WindowStart: &windowStart, WindowEnd: &windowEnd, Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable}},
		Availability: []usage.MetricAvailability{
			{MetricKey: metric.Key, State: usage.AvailabilityAvailable, CheckedAt: capturedAt, Provenance: usage.ProvenanceProvider},
			{MetricKey: usage.Registry()[1].Key, State: usage.AvailabilityUnsupported, CheckedAt: capturedAt, Provenance: usage.ProvenanceProvider},
		},
	})
	if err != nil {
		t.Fatalf("SaveUsageSnapshot() error = %v", err)
	}
	if snapshot.ID == "" || snapshot.ProfileID != "profile-2" || snapshot.Alias != "Work" || snapshot.Observations[0].ID == "" || snapshot.Availability[0].ID == "" {
		t.Fatalf("saved snapshot = %#v", snapshot)
	}

	var snapshots, observations, availability, provenance int
	for table, destination := range map[string]*int{"usage_snapshots": &snapshots, "usage_observations": &observations, "metric_availability": &availability, "metric_provenance": &provenance} {
		if err := stateStore.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	if snapshots != 1 || observations != 1 || availability != 2 || provenance != 1 {
		t.Fatalf("persisted counts = snapshots:%d observations:%d availability:%d provenance:%d", snapshots, observations, availability, provenance)
	}
	profiles, err := stateStore.ListProfiles(context.Background())
	if err != nil || len(profiles) != 2 || !profiles[0].Selected || profiles[0].ID != "profile-1" {
		t.Fatalf("profiles after refresh = %#v/%v, want profile-1 selected", profiles, err)
	}
}
