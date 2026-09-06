package usage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceRefreshUsesResolvedIdentityHomeAndPersistsNormalizedSnapshot(t *testing.T) {
	capturedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	store := &recordingStore{target: ProfileTarget{ID: "profile-1", Alias: "Work", IdentityHome: "/profiles/work"}}
	collector := &recordingCollector{snapshot: Snapshot{Source: SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: capturedAt, Observations: []Observation{}, Availability: []MetricAvailability{}}}
	service, err := NewService(store, collector, fixedClock{now: capturedAt})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Refresh(context.Background(), "Work", "/usr/bin/codex", "0.153.4")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if store.alias != "Work" || collector.request.IdentityHome != "/profiles/work" || collector.request.Executable != "/usr/bin/codex" || collector.request.CapturedAt != capturedAt {
		t.Fatalf("refresh inputs = alias:%q request:%#v", store.alias, collector.request)
	}
	if result.ID != "saved-snapshot" || store.snapshot.SourceVersion != "0.153.4" {
		t.Fatalf("result = %#v, persisted = %#v", result, store.snapshot)
	}
}

func TestServiceRefreshRecordsTemporaryFailureWithoutReplacingPriorEvidence(t *testing.T) {
	capturedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	observedAt := capturedAt.Add(-15 * time.Minute)
	store := &recordingStore{
		target:    ProfileTarget{ID: "profile-1", Alias: "Work", IdentityHome: "/profiles/work"},
		lastKnown: []Observation{{Metric: Registry()[0], Value: 0, ObservedAt: observedAt, WindowTimezone: "UTC", Provenance: ProvenanceProvider, Source: SourceCodexAppServer, SourceVersion: "0.153.3", CapturedAt: observedAt, Freshness: FreshnessFresh, Availability: AvailabilityAvailable}},
	}
	collector := &recordingCollector{err: ErrCollectionFailed}
	service, err := NewService(store, collector, fixedClock{now: capturedAt})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Refresh(context.Background(), "Work", "/usr/bin/codex", "0.153.4")
	if !errors.Is(err, ErrCollectionFailed) {
		t.Fatalf("Refresh() error = %v", err)
	}
	if len(store.snapshot.Observations) != 0 || len(store.snapshot.Availability) != len(Registry()) {
		t.Fatalf("persisted failure snapshot = %#v", store.snapshot)
	}
	for _, availability := range store.snapshot.Availability {
		if availability.State != AvailabilityTemporarilyUnavailable {
			t.Fatalf("availability = %#v", availability)
		}
		if availability.Reason != ReasonCollectionFailed || availability.CheckedAt != capturedAt {
			t.Fatalf("availability reason/time = %#v", availability)
		}
	}
	if len(result.Observations) != 1 || result.Observations[0].Value != 0 || result.Observations[0].Freshness != FreshnessStale || result.Observations[0].CaptureAgeSeconds != 900 {
		t.Fatalf("last-known projection = %#v", result.Observations)
	}
	collector.err = ErrSourceInvalid
	result, err = service.Refresh(context.Background(), "Work", "/usr/bin/codex", "0.153.4")
	if !errors.Is(err, ErrSourceInvalid) || result.Availability[0].Reason != ReasonMalformedSource {
		t.Fatalf("malformed-source result = %#v/%v", result, err)
	}
}

func TestServiceRefreshDerivesPartialStaleAndContradictoryEvidence(t *testing.T) {
	capturedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	metric := Registry()[0]
	store := &recordingStore{target: ProfileTarget{ID: "profile-1", Alias: "Work", IdentityHome: "/profiles/work"}}
	collector := &recordingCollector{snapshot: Snapshot{
		Source: SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: capturedAt,
		Observations: []Observation{
			{Metric: metric, Value: 20, ObservedAt: capturedAt.Add(-11 * time.Minute), Provenance: ProvenanceProvider, Source: SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: capturedAt.Add(-11 * time.Minute), Availability: AvailabilityAvailable},
			{Metric: metric, Value: 30, ObservedAt: capturedAt, Provenance: ProvenanceProvider, Source: SourceCodexAppServer, SourceVersion: "0.153.3", CapturedAt: capturedAt, Availability: AvailabilityAvailable},
		},
		Availability: []MetricAvailability{
			{MetricKey: metric.Key, State: AvailabilityAvailable, CheckedAt: capturedAt, Provenance: ProvenanceProvider},
			{MetricKey: Registry()[1].Key, State: AvailabilityUnsupported, Reason: ReasonUnsupported, CheckedAt: capturedAt, Provenance: ProvenanceProvider},
		},
	}}
	service, err := NewService(store, collector, fixedClock{now: capturedAt})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Refresh(context.Background(), "Work", "/usr/bin/codex", "0.153.4")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if result.Status != AvailabilityContradictory || result.Availability[0].State != AvailabilityContradictory || result.Availability[0].Reason != ReasonContradictory {
		t.Fatalf("contradictory result = %#v", result)
	}
	if result.Observations[0].Freshness != FreshnessStale || result.Observations[0].CaptureAgeSeconds != 660 || result.Observations[1].Freshness != FreshnessFresh {
		t.Fatalf("freshness = %#v", result.Observations)
	}
}

func TestServiceRefreshMarksOldEvidenceStaleWithinAPartialSnapshot(t *testing.T) {
	capturedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	metric := Registry()[0]
	store := &recordingStore{target: ProfileTarget{ID: "profile-1", Alias: "Work", IdentityHome: "/profiles/work"}}
	collector := &recordingCollector{snapshot: Snapshot{
		Source: SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: capturedAt,
		Observations: []Observation{{Metric: metric, Value: 0, ObservedAt: capturedAt.Add(-11 * time.Minute), CapturedAt: capturedAt.Add(-11 * time.Minute), Provenance: ProvenanceProvider, Availability: AvailabilityAvailable}},
		Availability: []MetricAvailability{
			{MetricKey: metric.Key, State: AvailabilityAvailable, CheckedAt: capturedAt, Provenance: ProvenanceProvider},
			{MetricKey: Registry()[1].Key, State: AvailabilityUnsupported, CheckedAt: capturedAt, Provenance: ProvenanceProvider},
		},
	}}
	service, err := NewService(store, collector, fixedClock{now: capturedAt})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Refresh(context.Background(), "Work", "/usr/bin/codex", "0.153.4")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != AvailabilityPartial || result.Availability[0].State != AvailabilityStale || result.Availability[0].Reason != ReasonStale || result.Observations[0].CaptureAgeSeconds != 660 {
		t.Fatalf("partial stale result = %#v", result)
	}
}

type recordingStore struct {
	target    ProfileTarget
	alias     string
	snapshot  Snapshot
	lastKnown []Observation
}

func (store *recordingStore) LastUsageObservations(_ context.Context, _ ProfileTarget) ([]Observation, error) {
	return append([]Observation(nil), store.lastKnown...), nil
}

func (store *recordingStore) LatestUsageSnapshot(_ context.Context, target ProfileTarget) (Snapshot, error) {
	snapshot := store.snapshot
	snapshot.ID, snapshot.ProfileID, snapshot.Alias = "saved-snapshot", target.ID, target.Alias
	snapshot.Observations = append([]Observation(nil), store.lastKnown...)
	return snapshot, nil
}

func (store *recordingStore) ResolveUsageProfile(_ context.Context, alias string) (ProfileTarget, error) {
	store.alias = alias
	return store.target, nil
}

func (store *recordingStore) SaveUsageSnapshot(_ context.Context, target ProfileTarget, snapshot Snapshot) (Snapshot, error) {
	store.snapshot = snapshot
	snapshot.ID, snapshot.ProfileID, snapshot.Alias = "saved-snapshot", target.ID, target.Alias
	return snapshot, nil
}

type recordingCollector struct {
	request  CollectionRequest
	snapshot Snapshot
	err      error
}

func (collector *recordingCollector) Collect(_ context.Context, request CollectionRequest) (Snapshot, error) {
	collector.request = request
	return collector.snapshot, collector.err
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }
