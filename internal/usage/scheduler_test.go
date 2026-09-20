package usage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestCollectionSettingsDefaultsAndProviderFloor(t *testing.T) {
	settings := DefaultCollectionSettings()
	if settings.ActiveInterval != 5*time.Minute || settings.IdleInterval != 30*time.Minute || settings.ProviderMinimum != 5*time.Minute {
		t.Fatalf("default settings = %#v", settings)
	}
	if err := ValidateCollectionSettings(CollectionSettings{ActiveInterval: 4 * time.Minute, IdleInterval: 30 * time.Minute}); !errors.Is(err, ErrScheduleInvalid) {
		t.Fatalf("ValidateCollectionSettings() error = %v, want provider-floor rejection", err)
	}
	if err := ValidateCollectionSettings(CollectionSettings{ActiveInterval: 5 * time.Minute, IdleInterval: 24*time.Hour + time.Minute}); !errors.Is(err, ErrScheduleInvalid) {
		t.Fatalf("ValidateCollectionSettings() error = %v, want bounded-interval rejection", err)
	}
}

func TestSchedulerUsesActiveIdleAndKnownResetWithoutInventingUnknownBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	reset := now.Add(8 * time.Minute)
	store := &scheduleStoreStub{
		settings: DefaultCollectionSettings(),
		targets: []ScheduleTarget{
			{Profile: ProfileTarget{ID: "active", Alias: "Active"}, Active: true, Latest: Snapshot{CapturedAt: now.Add(-5 * time.Minute)}},
			{Profile: ProfileTarget{ID: "idle", Alias: "Idle"}, Latest: Snapshot{CapturedAt: now, Observations: []Observation{{WindowEnd: &reset}}}},
			{Profile: ProfileTarget{ID: "unknown", Alias: "Unknown"}, Latest: Snapshot{CapturedAt: now.Add(-25 * time.Minute)}},
		},
	}
	collector := &scheduledRefresherStub{}
	scheduler, err := NewScheduler(store, collector, scheduleClock{now: now}, func(time.Duration) time.Duration { return 0 })
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}

	result, err := scheduler.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if len(collector.calls) != 1 || collector.calls[0].alias != "Active" || collector.calls[0].trigger != TriggerPeriodicActive {
		t.Fatalf("immediate calls = %#v, want active profile only", collector.calls)
	}
	if result.Collected != 1 || result.Coalesced {
		t.Fatalf("result = %#v", result)
	}
	if got := store.states["idle"].NextAttemptAt; !got.Equal(reset) {
		t.Fatalf("idle next attempt = %s, want known reset %s", got, reset)
	}
	if got := store.states["unknown"].NextAttemptAt; !got.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("unknown next attempt = %s, want ordinary idle interval at %s", got, now.Add(5*time.Minute))
	}
}

func TestSchedulerRefreshesAtApproachingResetBoundaryBeforeLongIdleInterval(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	reset := now.Add(2 * time.Hour)
	settings := CollectionSettings{ActiveInterval: DefaultActiveInterval, IdleInterval: MaximumInterval, ProviderMinimum: ProviderSafeMinimum}
	store := &scheduleStoreStub{settings: settings, targets: []ScheduleTarget{{
		Profile: ProfileTarget{ID: "idle", Alias: "Idle"},
		Latest:  Snapshot{CapturedAt: now, Observations: []Observation{{WindowEnd: &reset}}},
	}}}
	collector := &scheduledRefresherStub{}
	scheduler, err := NewScheduler(store, collector, scheduleClock{now: now}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := scheduler.Tick(context.Background()); err != nil || result.Collected != 0 {
		t.Fatalf("initial Tick() = %#v/%v", result, err)
	}
	warningBoundary := reset.Add(-ResetApproachingLead)
	if got := store.states["idle"].NextAttemptAt; !got.Equal(warningBoundary) {
		t.Fatalf("next attempt = %s, want approaching-reset boundary %s", got, warningBoundary)
	}

	store.targets[0].State = store.states["idle"]
	collector.snapshot = Snapshot{CapturedAt: warningBoundary, Observations: []Observation{{WindowEnd: &reset}}}
	scheduler.clock = scheduleClock{now: warningBoundary}
	if result, err := scheduler.Tick(context.Background()); err != nil || result.Collected != 1 || len(collector.calls) != 1 || collector.calls[0].trigger != TriggerPeriodicReset {
		t.Fatalf("boundary Tick() = %#v/%v, calls %#v", result, err, collector.calls)
	}
	if got := store.states["idle"].NextAttemptAt; !got.Equal(reset) {
		t.Fatalf("post-warning next attempt = %s, want reset %s", got, reset)
	}
}

func TestSchedulerPersistsExponentialBackoffWithBoundedJitterAndRecovers(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	store := &scheduleStoreStub{settings: DefaultCollectionSettings(), targets: []ScheduleTarget{{Profile: ProfileTarget{ID: "work", Alias: "Work"}}}}
	collector := &scheduledRefresherStub{err: ErrCollectionFailed}
	scheduler, err := NewScheduler(store, collector, scheduleClock{now: now}, func(value time.Duration) time.Duration { return value / 10 })
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}

	if _, err := scheduler.Tick(context.Background()); err != nil {
		t.Fatalf("first Tick() error = %v", err)
	}
	state := store.states["work"]
	if state.ConsecutiveFailures != 1 || !state.NextAttemptAt.Equal(now.Add(5*time.Minute+30*time.Second)) || state.LastOutcome != ScheduleOutcomeFailed {
		t.Fatalf("first failure state = %#v", state)
	}

	store.targets[0].State = state
	scheduler.clock = scheduleClock{now: state.NextAttemptAt}
	if _, err := scheduler.Tick(context.Background()); err != nil {
		t.Fatalf("second Tick() error = %v", err)
	}
	state = store.states["work"]
	if state.ConsecutiveFailures != 2 || !state.NextAttemptAt.Equal(now.Add(5*time.Minute+30*time.Second+11*time.Minute)) {
		t.Fatalf("second failure state = %#v", state)
	}

	collector.err = nil
	store.targets[0].State = state
	scheduler.clock = scheduleClock{now: state.NextAttemptAt}
	if _, err := scheduler.Tick(context.Background()); err != nil {
		t.Fatalf("recovery Tick() error = %v", err)
	}
	state = store.states["work"]
	if state.ConsecutiveFailures != 0 || state.LastOutcome != ScheduleOutcomeSucceeded || !state.NextAttemptAt.Equal(scheduler.clock.Now().Add(33*time.Minute)) {
		t.Fatalf("recovery state = %#v", state)
	}
}

func TestSchedulerCoalescesOverlappingTicks(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	store := &scheduleStoreStub{settings: DefaultCollectionSettings(), targets: []ScheduleTarget{{Profile: ProfileTarget{ID: "work", Alias: "Work"}}}}
	started := make(chan struct{})
	release := make(chan struct{})
	collector := &scheduledRefresherStub{started: started, release: release}
	scheduler, err := NewScheduler(store, collector, scheduleClock{now: now}, nil)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, tickErr := scheduler.Tick(context.Background())
		done <- tickErr
	}()
	<-started
	result, err := scheduler.Tick(context.Background())
	if err != nil {
		t.Fatalf("overlapping Tick() error = %v", err)
	}
	if !result.Coalesced || result.Collected != 0 {
		t.Fatalf("overlapping result = %#v", result)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first Tick() error = %v", err)
	}
	if len(collector.calls) != 1 {
		t.Fatalf("refresh calls = %d, want one", len(collector.calls))
	}
}

func TestSchedulerDoesNoCollectionOrStateWriteBeforePersistedAttemptIsDue(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	store := &scheduleStoreStub{settings: DefaultCollectionSettings(), targets: []ScheduleTarget{{
		Profile: ProfileTarget{ID: "work", Alias: "Work"},
		State:   ScheduleState{NextAttemptAt: now.Add(5 * time.Minute)},
	}}}
	collector := &scheduledRefresherStub{}
	scheduler, err := NewScheduler(store, collector, scheduleClock{now: now}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := scheduler.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(collector.calls) != 0 || store.saves != 0 {
		t.Fatalf("idle work = %d collections and %d state writes, want none", len(collector.calls), store.saves)
	}
}

func TestSchedulerReconcilesPersistedAttemptWithCurrentSchedulingEvidence(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	reset := now.Add(8 * time.Minute)
	tests := []struct {
		name     string
		settings CollectionSettings
		target   ScheduleTarget
		want     time.Time
	}{
		{
			name:     "idle to active",
			settings: DefaultCollectionSettings(),
			target: ScheduleTarget{
				Profile: ProfileTarget{ID: "work", Alias: "Work"},
				Active:  true,
				Latest:  Snapshot{CapturedAt: now},
				State:   ScheduleState{NextAttemptAt: now.Add(30 * time.Minute)},
			},
			want: now.Add(5 * time.Minute),
		},
		{
			name:     "active to idle with newer snapshot",
			settings: DefaultCollectionSettings(),
			target: ScheduleTarget{
				Profile: ProfileTarget{ID: "work", Alias: "Work"},
				Latest:  Snapshot{CapturedAt: now},
				State: ScheduleState{
					LastAttemptAt: now.Add(-2 * time.Minute),
					NextAttemptAt: now.Add(3 * time.Minute),
					LastOutcome:   ScheduleOutcomeSucceeded,
				},
			},
			want: now.Add(30 * time.Minute),
		},
		{
			name: "active interval changed",
			settings: CollectionSettings{
				ActiveInterval:  10 * time.Minute,
				IdleInterval:    DefaultIdleInterval,
				ProviderMinimum: ProviderSafeMinimum,
			},
			target: ScheduleTarget{
				Profile: ProfileTarget{ID: "work", Alias: "Work"},
				Active:  true,
				Latest:  Snapshot{CapturedAt: now},
				State:   ScheduleState{NextAttemptAt: now.Add(5 * time.Minute)},
			},
			want: now.Add(10 * time.Minute),
		},
		{
			name:     "new known reset",
			settings: DefaultCollectionSettings(),
			target: ScheduleTarget{
				Profile: ProfileTarget{ID: "work", Alias: "Work"},
				Latest:  Snapshot{CapturedAt: now, Observations: []Observation{{WindowEnd: &reset}}},
				State:   ScheduleState{NextAttemptAt: now.Add(30 * time.Minute)},
			},
			want: reset,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &scheduleStoreStub{settings: test.settings, targets: []ScheduleTarget{test.target}}
			scheduler, err := NewScheduler(store, &scheduledRefresherStub{}, scheduleClock{now: now}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if result, err := scheduler.Tick(context.Background()); err != nil || result.Collected != 0 {
				t.Fatalf("Tick() = %#v, %v", result, err)
			}
			if got := store.states["work"].NextAttemptAt; !got.Equal(test.want) {
				t.Fatalf("reconciled next attempt = %s, want %s", got, test.want)
			}
			if store.saves != 1 {
				t.Fatalf("schedule writes = %d, want one reconciliation write", store.saves)
			}
		})
	}
}

func TestSchedulerPreservesPersistedFailureBackoffDuringReconciliation(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	reset := now.Add(6 * time.Minute)
	backoff := now.Add(20 * time.Minute)
	store := &scheduleStoreStub{settings: DefaultCollectionSettings(), targets: []ScheduleTarget{{
		Profile: ProfileTarget{ID: "work", Alias: "Work"},
		Active:  true,
		Latest:  Snapshot{CapturedAt: now, Observations: []Observation{{WindowEnd: &reset}}},
		State: ScheduleState{
			NextAttemptAt:       backoff,
			ConsecutiveFailures: 2,
			LastOutcome:         ScheduleOutcomeFailed,
		},
	}}}
	scheduler, err := NewScheduler(store, &scheduledRefresherStub{}, scheduleClock{now: now}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := scheduler.Tick(context.Background()); err != nil || result.Collected != 0 {
		t.Fatalf("Tick() = %#v, %v", result, err)
	}
	if store.saves != 0 {
		t.Fatalf("schedule writes = %d, want preserved failure backoff", store.saves)
	}
}

func TestSchedulerBoundsCollectionsAndScheduleStateWorkPerTick(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	targets := make([]ScheduleTarget, MaxScheduleTargets+10)
	for index := range targets {
		targets[index] = ScheduleTarget{Profile: ProfileTarget{ID: fmt.Sprintf("profile-%03d", index), Alias: fmt.Sprintf("Profile %03d", index)}}
	}
	store := &scheduleStoreStub{settings: DefaultCollectionSettings(), targets: targets}
	collector := &scheduledRefresherStub{}
	scheduler, err := NewScheduler(store, collector, scheduleClock{now: now}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scheduler.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Collected != MaxCollectionsPerTick || len(collector.calls) != MaxCollectionsPerTick {
		t.Fatalf("collections = %#v/%d", result, len(collector.calls))
	}
	if store.saves != MaxCollectionsPerTick {
		t.Fatalf("schedule writes = %d, want %d completed collection states", store.saves, MaxCollectionsPerTick)
	}
	for index := range targets {
		targets[index].Latest = Snapshot{CapturedAt: now}
	}
	idleStore := &scheduleStoreStub{settings: DefaultCollectionSettings(), targets: targets}
	idleScheduler, err := NewScheduler(idleStore, &scheduledRefresherStub{}, scheduleClock{now: now}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := idleScheduler.Tick(context.Background()); err != nil || result.Collected != 0 {
		t.Fatalf("idle bounded tick = %#v, %v", result, err)
	}
	if idleStore.saves != MaxScheduleTargets {
		t.Fatalf("initial schedule writes = %d, want bounded %d", idleStore.saves, MaxScheduleTargets)
	}
	if _, err := idleScheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	lastID := targets[len(targets)-1].Profile.ID
	if _, ok := idleStore.states[lastID]; !ok {
		t.Fatalf("second bounded tick did not rotate far enough to inspect %s", lastID)
	}
}

func TestSchedulerPreservesResetTriggerAcrossRestartAndSchedulesReturnedReset(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	firstReset := now
	nextReset := now.Add(8 * time.Minute)
	store := &scheduleStoreStub{settings: DefaultCollectionSettings(), targets: []ScheduleTarget{{
		Profile: ProfileTarget{ID: "work", Alias: "Work"},
		Latest:  Snapshot{CapturedAt: now.Add(-10 * time.Minute), Observations: []Observation{{WindowEnd: &firstReset}}},
		State:   ScheduleState{NextAttemptAt: firstReset},
	}}}
	collector := &scheduledRefresherStub{snapshot: Snapshot{CapturedAt: now, Observations: []Observation{{WindowEnd: &nextReset}}}}
	scheduler, err := NewScheduler(store, collector, scheduleClock{now: now}, nil)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}

	if _, err := scheduler.Tick(context.Background()); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if len(collector.calls) != 1 || collector.calls[0].trigger != TriggerPeriodicReset {
		t.Fatalf("calls = %#v, want persisted reset trigger", collector.calls)
	}
	if got := store.states["work"].NextAttemptAt; !got.Equal(nextReset) {
		t.Fatalf("next attempt = %s, want returned reset %s", got, nextReset)
	}
}

type scheduleClock struct{ now time.Time }

func (clock scheduleClock) Now() time.Time { return clock.now }

type scheduleStoreStub struct {
	settings CollectionSettings
	targets  []ScheduleTarget
	states   map[string]ScheduleState
	saves    int
}

func (store *scheduleStoreStub) CollectionSettings(context.Context) (CollectionSettings, error) {
	return store.settings, nil
}

func (store *scheduleStoreStub) CollectionScheduleTargets(context.Context) ([]ScheduleTarget, error) {
	result := make([]ScheduleTarget, len(store.targets))
	copy(result, store.targets)
	return result, nil
}

func (store *scheduleStoreStub) SaveCollectionScheduleState(_ context.Context, profileID string, state ScheduleState) error {
	if store.states == nil {
		store.states = make(map[string]ScheduleState)
	}
	store.states[profileID] = state
	store.saves++
	return nil
}

type scheduledCall struct{ alias, trigger string }

type scheduledRefresherStub struct {
	mu       sync.Mutex
	calls    []scheduledCall
	err      error
	started  chan struct{}
	release  chan struct{}
	snapshot Snapshot
}

func (collector *scheduledRefresherStub) Refresh(_ context.Context, alias, trigger string) (Snapshot, error) {
	collector.mu.Lock()
	collector.calls = append(collector.calls, scheduledCall{alias: alias, trigger: trigger})
	collector.mu.Unlock()
	if collector.started != nil {
		close(collector.started)
		<-collector.release
	}
	return collector.snapshot, collector.err
}
