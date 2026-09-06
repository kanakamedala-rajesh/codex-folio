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
	store := &recordingStore{target: ProfileTarget{ID: "profile-1", Alias: "Work", IdentityHome: "/profiles/work"}}
	collector := &recordingCollector{err: ErrCollectionFailed}
	service, err := NewService(store, collector, fixedClock{now: capturedAt})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background(), "Work", "/usr/bin/codex", "0.153.4"); !errors.Is(err, ErrCollectionFailed) {
		t.Fatalf("Refresh() error = %v", err)
	}
	if len(store.snapshot.Observations) != 0 || len(store.snapshot.Availability) != len(Registry()) {
		t.Fatalf("persisted failure snapshot = %#v", store.snapshot)
	}
	for _, availability := range store.snapshot.Availability {
		if availability.State != AvailabilityTemporarilyUnavailable {
			t.Fatalf("availability = %#v", availability)
		}
	}
}

type recordingStore struct {
	target   ProfileTarget
	alias    string
	snapshot Snapshot
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
