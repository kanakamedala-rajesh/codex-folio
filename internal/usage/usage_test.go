package usage

import (
	"context"
	"errors"
	"sync"
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
	result, err := service.Refresh(context.Background(), "Work", "/usr/bin/codex", "0.153.4", TriggerExplicitRefresh)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if store.alias != "Work" || collector.request.IdentityHome != "/profiles/work" || collector.request.Executable != "/usr/bin/codex" || collector.request.CapturedAt != capturedAt {
		t.Fatalf("refresh inputs = alias:%q request:%#v", store.alias, collector.request)
	}
	if result.ID != "saved-snapshot" || store.snapshot.SourceVersion != "0.153.4" || store.snapshot.TriggerReason != TriggerExplicitRefresh {
		t.Fatalf("result = %#v, persisted = %#v", result, store.snapshot)
	}
}

func TestServiceRefreshSerializesConcurrentTriggers(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	collector := &serialCollector{started: started, release: release}
	service, err := NewService(&recordingStore{target: ProfileTarget{ID: "profile-1", Alias: "Work", IdentityHome: "/profiles/work"}}, collector, fixedClock{now: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}

	var group sync.WaitGroup
	group.Add(2)
	for _, trigger := range []string{TriggerDashboardOpen, TriggerDashboardRefresh} {
		go func() {
			defer group.Done()
			_, _ = service.Refresh(context.Background(), "Work", "/usr/bin/codex", "0.153.4", trigger)
		}()
	}
	<-started
	select {
	case <-started:
		t.Fatal("concurrent collection was not serialized")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	group.Wait()
	if collector.maxActive != 1 {
		t.Fatalf("maximum concurrent collections = %d, want 1", collector.maxActive)
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
	result, err := service.Refresh(context.Background(), "Work", "/usr/bin/codex", "0.153.4", TriggerExplicitRefresh)
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
	result, err = service.Refresh(context.Background(), "Work", "/usr/bin/codex", "0.153.4", TriggerExplicitRefresh)
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
	result, err := service.Refresh(context.Background(), "Work", "/usr/bin/codex", "0.153.4", TriggerExplicitRefresh)
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
	result, err := service.Refresh(context.Background(), "Work", "/usr/bin/codex", "0.153.4", TriggerExplicitRefresh)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != AvailabilityPartial || result.Availability[0].State != AvailabilityStale || result.Availability[0].Reason != ReasonStale || result.Observations[0].CaptureAgeSeconds != 660 {
		t.Fatalf("partial stale result = %#v", result)
	}
}

func TestCombinePreservesLimitsAndAggregatesOnlyCompatibleAbsoluteMetrics(t *testing.T) {
	start := time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	percentage := Registry()[0]
	absolute := Metric{Key: "test.tokens", ValueKind: "count", Unit: "tokens", SourceClass: ProvenanceLocal, Scope: "login_identity", Aggregation: AggregationSum}
	incompatible := absolute
	incompatible.Unit = "credits"
	snapshots := []Snapshot{
		{ProfileID: "profile-1", Alias: "Personal", LoginIdentity: "login-1", Workspace: "workspace-1", Observations: []Observation{
			{Metric: percentage, Value: 25, WindowStart: &start, WindowEnd: &end, Source: SourceCodexAppServer},
			{Metric: absolute, Value: 10, WindowStart: &start, WindowEnd: &end, Source: SourceLocalMetadata},
		}},
		{ProfileID: "profile-2", Alias: "Work", LoginIdentity: "login-2", Workspace: "workspace-2", Observations: []Observation{
			{Metric: percentage, Value: 50, WindowStart: &start, WindowEnd: &end, Source: SourceCodexAppServer},
			{Metric: absolute, Value: 20, WindowStart: &start, WindowEnd: &end, Source: SourceLocalMetadata},
			{Metric: incompatible, Value: 1000, WindowStart: &start, WindowEnd: &end, Source: SourceLocalMetadata},
		}},
	}

	view := combineWithRegistry(snapshots, 2, append(Registry(), absolute))
	if len(view.Profiles) != 2 || view.EligibleProfileCount != 2 {
		t.Fatalf("combined profiles/count = %#v/%d", view.Profiles, view.EligibleProfileCount)
	}
	if len(view.Aggregates) != 1 || view.Aggregates[0].Metric != absolute || view.Aggregates[0].Value != 30 {
		t.Fatalf("aggregates = %#v, want one 30-token total and no percentage total", view.Aggregates)
	}
}

func TestCombineDeduplicatesSharedOverlappingEvidenceAndMarksConflictsAmbiguous(t *testing.T) {
	start := time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	absolute := Metric{Key: "test.tokens", ValueKind: "count", Unit: "tokens", SourceClass: ProvenanceLocal, Scope: "login_identity", Aggregation: AggregationSum}
	observation := func(value float64) Observation {
		return Observation{Metric: absolute, Value: value, WindowStart: &start, WindowEnd: &end, Source: SourceLocalMetadata}
	}
	snapshots := []Snapshot{
		{ProfileID: "profile-1", LoginIdentity: "login-1", Workspace: "workspace-1", Observations: []Observation{observation(10)}},
		{ProfileID: "profile-2", LoginIdentity: "login-1", Workspace: "workspace-1", Observations: []Observation{observation(10)}},
		{ProfileID: "profile-3", LoginIdentity: "login-1", Workspace: "workspace-1", Observations: []Observation{observation(12)}},
		{ProfileID: "profile-4", Observations: []Observation{observation(5)}},
	}

	view := combineWithRegistry(snapshots, 4, append(Registry(), absolute))
	if len(view.Aggregates) != 0 || len(view.AmbiguousMetricKeys) != 1 || view.AmbiguousMetricKeys[0] != absolute.Key {
		t.Fatalf("conflicting combined view = %#v", view)
	}

	view = combineWithRegistry(snapshots[:2], 2, append(Registry(), absolute))
	if len(view.Aggregates) != 1 || view.Aggregates[0].Value != 10 {
		t.Fatalf("deduplicated shared evidence = %#v", view.Aggregates)
	}

	workspaceMetric := absolute
	workspaceMetric.Scope = "workspace"
	workspaceSnapshots := []Snapshot{
		{ProfileID: "profile-1", LoginIdentity: "login-1", Workspace: "workspace-1", Observations: []Observation{{Metric: workspaceMetric, Value: 10, WindowStart: &start, WindowEnd: &end}}},
		{ProfileID: "profile-2", LoginIdentity: "login-1", Workspace: "workspace-1", Observations: []Observation{{Metric: workspaceMetric, Value: 10, WindowStart: &start, WindowEnd: &end}}},
	}
	view = combineWithRegistry(workspaceSnapshots, 2, append(Registry(), workspaceMetric))
	if len(view.Aggregates) != 1 || view.Aggregates[0].Value != 10 {
		t.Fatalf("deduplicated shared workspace evidence = %#v", view.Aggregates)
	}
	workspaceMetric.Scope = "session"
	for index := range workspaceSnapshots {
		workspaceSnapshots[index].Observations[0].Metric = workspaceMetric
	}
	view = combineWithRegistry(workspaceSnapshots, 2, append(Registry(), workspaceMetric))
	if len(view.Aggregates) != 1 || view.Aggregates[0].Value != 20 {
		t.Fatalf("profile-scoped session evidence = %#v", view.Aggregates)
	}

	missingScopes := []Snapshot{
		{ProfileID: "profile-1", Observations: []Observation{observation(10)}},
		{ProfileID: "profile-2", Observations: []Observation{observation(5)}},
	}
	view = combineWithRegistry(missingScopes, 2, append(Registry(), absolute))
	if len(view.Aggregates) != 1 || view.Aggregates[0].Value != 15 {
		t.Fatalf("missing stable identifiers were deduplicated = %#v", view.Aggregates)
	}

	disjointStart, disjointEnd := end, end.Add(time.Hour)
	disjoint := observation(5)
	disjoint.WindowStart, disjoint.WindowEnd = &disjointStart, &disjointEnd
	view = combineWithRegistry([]Snapshot{snapshots[0], {ProfileID: "profile-2", LoginIdentity: "login-1", Workspace: "workspace-1", Observations: []Observation{disjoint}}}, 2, append(Registry(), absolute))
	if len(view.Aggregates) != 1 || view.Aggregates[0].Value != 15 {
		t.Fatalf("disjoint shared windows = %#v", view.Aggregates)
	}

	bridgeStart, bridgeEnd := start.Add(30*time.Minute), end.Add(30*time.Minute)
	conflictStart, conflictEnd := end.Add(15*time.Minute), end.Add(time.Hour)
	bridge, conflict := observation(10), observation(12)
	bridge.WindowStart, bridge.WindowEnd = &bridgeStart, &bridgeEnd
	conflict.WindowStart, conflict.WindowEnd = &conflictStart, &conflictEnd
	view = combineWithRegistry([]Snapshot{
		snapshots[0],
		{ProfileID: "profile-2", LoginIdentity: "login-1", Observations: []Observation{bridge}},
		{ProfileID: "profile-3", LoginIdentity: "login-1", Observations: []Observation{conflict}},
	}, 3, append(Registry(), absolute))
	if len(view.Aggregates) != 0 || len(view.AmbiguousMetricKeys) != 1 {
		t.Fatalf("transitive overlapping conflict = %#v", view)
	}
}

func TestServiceViewDefaultsToSelectedProfileAndCombinesOnlyWhenExplicit(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	profiles := []ProfileTarget{
		{ID: "profile-1", Alias: "Personal", Selected: true, Eligible: true},
		{ID: "profile-2", Alias: "Work", Eligible: true},
	}
	state := &recordingStore{profiles: profiles, snapshots: map[string]Snapshot{
		"profile-1": {ID: "snapshot-1", ProfileID: "profile-1", Alias: "Personal", CapturedAt: now, Availability: []MetricAvailability{}},
		"profile-2": {ID: "snapshot-2", ProfileID: "profile-2", Alias: "Work", CapturedAt: now, Availability: []MetricAvailability{}},
	}}
	service, err := NewService(state, &recordingCollector{}, fixedClock{now: now})
	if err != nil {
		t.Fatal(err)
	}

	selected, err := service.View(context.Background(), "", true)
	if err != nil || selected.Scope != ScopeSelectedProfile || selected.EligibleProfileCount != 2 || len(selected.Profiles) != 1 || selected.Profiles[0].Alias != "Personal" {
		t.Fatalf("selected view/error = %#v/%v", selected, err)
	}
	combined, err := service.View(context.Background(), ScopeCombinedIdentity, true)
	if err != nil || combined.Scope != ScopeCombinedIdentity || len(combined.Profiles) != 2 || combined.EligibleProfileCount != 2 {
		t.Fatalf("combined view/error = %#v/%v", combined, err)
	}
	if state.selectionWrites != 0 {
		t.Fatalf("view changed Selected Profile %d times", state.selectionWrites)
	}
}

type recordingStore struct {
	target          ProfileTarget
	alias           string
	snapshot        Snapshot
	lastKnown       []Observation
	profiles        []ProfileTarget
	snapshots       map[string]Snapshot
	selectionWrites int
}

func (store *recordingStore) ListUsageProfiles(context.Context) ([]ProfileTarget, error) {
	return append([]ProfileTarget(nil), store.profiles...), nil
}

func (store *recordingStore) LastUsageObservations(_ context.Context, _ ProfileTarget) ([]Observation, error) {
	return append([]Observation(nil), store.lastKnown...), nil
}

func (store *recordingStore) LatestUsageSnapshot(_ context.Context, target ProfileTarget) (Snapshot, error) {
	if snapshot, ok := store.snapshots[target.ID]; ok {
		return snapshot, nil
	}
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

type serialCollector struct {
	mu        sync.Mutex
	active    int
	maxActive int
	started   chan struct{}
	release   chan struct{}
}

func (collector *serialCollector) Collect(_ context.Context, request CollectionRequest) (Snapshot, error) {
	collector.mu.Lock()
	collector.active++
	if collector.active > collector.maxActive {
		collector.maxActive = collector.active
	}
	collector.mu.Unlock()
	collector.started <- struct{}{}
	<-collector.release
	collector.mu.Lock()
	collector.active--
	collector.mu.Unlock()
	return NewUnavailableSnapshot(request.SourceVersion, request.CapturedAt, AvailabilityUnsupported, ReasonUnsupported), nil
}

func (collector *recordingCollector) Collect(_ context.Context, request CollectionRequest) (Snapshot, error) {
	collector.request = request
	return collector.snapshot, collector.err
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }
