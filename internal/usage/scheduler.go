package usage

import (
	"context"
	"errors"
	"sort"
	"sync/atomic"
	"time"
)

const (
	DefaultActiveInterval = 5 * time.Minute
	DefaultIdleInterval   = 30 * time.Minute
	ProviderSafeMinimum   = 5 * time.Minute
	MaximumInterval       = 24 * time.Hour
	MaximumBackoff        = 6 * time.Hour
	MaxCollectionsPerTick = 4
	MaxScheduleTargets    = 64
	ResetApproachingLead  = 30 * time.Minute

	ScheduleOutcomeSucceeded = "succeeded"
	ScheduleOutcomeFailed    = "failed"
)

var ErrScheduleInvalid = errors.New("collection schedule is invalid")

type CollectionSettings struct {
	ActiveInterval  time.Duration `json:"active_interval"`
	IdleInterval    time.Duration `json:"idle_interval"`
	ProviderMinimum time.Duration `json:"provider_minimum"`
}

func DefaultCollectionSettings() CollectionSettings {
	return CollectionSettings{ActiveInterval: DefaultActiveInterval, IdleInterval: DefaultIdleInterval, ProviderMinimum: ProviderSafeMinimum}
}

func ValidateCollectionSettings(settings CollectionSettings) error {
	if settings.ProviderMinimum == 0 {
		settings.ProviderMinimum = ProviderSafeMinimum
	}
	if settings.ProviderMinimum != ProviderSafeMinimum || settings.ActiveInterval < settings.ProviderMinimum || settings.IdleInterval < settings.ProviderMinimum || settings.ActiveInterval > MaximumInterval || settings.IdleInterval > MaximumInterval {
		return ErrScheduleInvalid
	}
	return nil
}

type ScheduleState struct {
	LastAttemptAt       time.Time
	NextAttemptAt       time.Time
	ConsecutiveFailures int
	LastOutcome         string
}

type ScheduleTarget struct {
	Profile ProfileTarget
	Active  bool
	Latest  Snapshot
	State   ScheduleState
}

type ScheduleStore interface {
	CollectionSettings(context.Context) (CollectionSettings, error)
	CollectionScheduleTargets(context.Context) ([]ScheduleTarget, error)
	SaveCollectionScheduleState(context.Context, string, ScheduleState) error
}

type ScheduledRefresher interface {
	Refresh(context.Context, string, string) (Snapshot, error)
}

type Jitter func(time.Duration) time.Duration

type Scheduler struct {
	store     ScheduleStore
	refresher ScheduledRefresher
	clock     Clock
	jitter    Jitter
	running   atomic.Bool
	cursor    atomic.Uint64
}

type TickResult struct {
	Collected int  `json:"collected"`
	Failed    int  `json:"failed"`
	Coalesced bool `json:"coalesced"`
}

func NewScheduler(store ScheduleStore, refresher ScheduledRefresher, clock Clock, jitter Jitter) (*Scheduler, error) {
	if store == nil || refresher == nil || clock == nil {
		return nil, ErrScheduleInvalid
	}
	if jitter == nil {
		jitter = func(time.Duration) time.Duration { return 0 }
	}
	return &Scheduler{store: store, refresher: refresher, clock: clock, jitter: jitter}, nil
}

func (scheduler *Scheduler) Tick(ctx context.Context) (TickResult, error) {
	if scheduler == nil || scheduler.store == nil || scheduler.refresher == nil || scheduler.clock == nil {
		return TickResult{}, ErrScheduleInvalid
	}
	if !scheduler.running.CompareAndSwap(false, true) {
		return TickResult{Coalesced: true}, nil
	}
	defer scheduler.running.Store(false)

	now := scheduler.clock.Now().UTC()
	if now.IsZero() {
		return TickResult{}, ErrScheduleInvalid
	}
	settings, err := scheduler.store.CollectionSettings(ctx)
	if err != nil {
		return TickResult{}, err
	}
	if err := ValidateCollectionSettings(settings); err != nil {
		return TickResult{}, err
	}
	targets, err := scheduler.store.CollectionScheduleTargets(ctx)
	if err != nil {
		return TickResult{}, err
	}
	sort.SliceStable(targets, func(left, right int) bool {
		if targets[left].Active != targets[right].Active {
			return targets[left].Active
		}
		return targets[left].Profile.Alias < targets[right].Profile.Alias
	})

	result := TickResult{}
	limit := min(len(targets), MaxScheduleTargets)
	start := 0
	if len(targets) > 0 {
		start = int(scheduler.cursor.Load() % uint64(len(targets)))
	}
	inspected := 0
	defer func() { scheduler.cursor.Add(uint64(inspected)) }()
	for inspected < limit {
		if result.Collected >= MaxCollectionsPerTick {
			break
		}
		target := targets[(start+inspected)%len(targets)]
		inspected++
		dueAt, trigger := nextScheduledAttempt(target, settings, now)
		if now.Before(dueAt) {
			if target.State.NextAttemptAt.IsZero() || !target.State.NextAttemptAt.Equal(dueAt) {
				target.State.NextAttemptAt = dueAt
				if err := scheduler.store.SaveCollectionScheduleState(ctx, target.Profile.ID, target.State); err != nil {
					return result, err
				}
			}
			continue
		}

		snapshot, refreshErr := scheduler.refresher.Refresh(ctx, target.Profile.Alias, trigger)
		result.Collected++
		target.State.LastAttemptAt = now
		if refreshErr != nil {
			result.Failed++
			target.State.ConsecutiveFailures++
			target.State.LastOutcome = ScheduleOutcomeFailed
			backoff := exponentialBackoff(target.State.ConsecutiveFailures, settings.ProviderMinimum)
			target.State.NextAttemptAt = now.Add(backoff + scheduler.boundedJitter(backoff))
		} else {
			target.State.ConsecutiveFailures = 0
			target.State.LastOutcome = ScheduleOutcomeSucceeded
			interval := settings.IdleInterval
			if target.Active {
				interval = settings.ActiveInterval
			}
			if snapshot.CapturedAt.IsZero() {
				target.State.NextAttemptAt = now.Add(interval + scheduler.boundedJitter(interval))
			} else {
				target.Latest = snapshot
				target.State.NextAttemptAt, _ = nextScheduledAttempt(targetWithoutPersistedAttempt(target), settings, now)
				if target.State.NextAttemptAt.Equal(snapshot.CapturedAt.UTC().Add(interval)) {
					target.State.NextAttemptAt = target.State.NextAttemptAt.Add(scheduler.boundedJitter(interval))
				}
			}
		}
		if err := scheduler.store.SaveCollectionScheduleState(ctx, target.Profile.ID, target.State); err != nil {
			return result, err
		}
	}
	return result, nil
}

func nextScheduledAttempt(target ScheduleTarget, settings CollectionSettings, now time.Time) (time.Time, string) {
	if target.State.NextAttemptAt.IsZero() {
		return scheduledAttemptFromEvidence(target, settings, now)
	}
	for _, observation := range target.Latest.Observations {
		if observation.WindowEnd != nil && (observation.WindowEnd.UTC().Equal(target.State.NextAttemptAt.UTC()) || observation.WindowEnd.UTC().Add(-ResetApproachingLead).Equal(target.State.NextAttemptAt.UTC())) {
			return target.State.NextAttemptAt, TriggerPeriodicReset
		}
	}
	if target.State.LastOutcome == ScheduleOutcomeFailed && target.State.ConsecutiveFailures > 0 {
		return target.State.NextAttemptAt, periodicTrigger(target)
	}
	if target.Latest.CapturedAt.IsZero() {
		return target.State.NextAttemptAt, periodicTrigger(target)
	}

	dueAt, trigger := scheduledAttemptFromEvidence(target, settings, now)
	if trigger == TriggerPeriodicReset {
		if target.State.NextAttemptAt.Equal(dueAt) {
			return target.State.NextAttemptAt, trigger
		}
		return dueAt, trigger
	}
	interval := settings.IdleInterval
	if target.Active {
		interval = settings.ActiveInterval
	}
	if !target.State.NextAttemptAt.Before(dueAt) && !target.State.NextAttemptAt.After(dueAt.Add(interval/10)) {
		return target.State.NextAttemptAt, trigger
	}
	return dueAt, trigger
}

func scheduledAttemptFromEvidence(target ScheduleTarget, settings CollectionSettings, now time.Time) (time.Time, string) {
	interval := settings.IdleInterval
	trigger := TriggerPeriodicIdle
	if target.Active {
		interval = settings.ActiveInterval
		trigger = TriggerPeriodicActive
	}
	if target.Latest.CapturedAt.IsZero() {
		return now, trigger
	}
	dueAt := target.Latest.CapturedAt.UTC().Add(interval)
	floorAt := target.Latest.CapturedAt.UTC().Add(settings.ProviderMinimum)
	for _, observation := range target.Latest.Observations {
		if observation.WindowEnd == nil {
			continue
		}
		reset := observation.WindowEnd.UTC()
		resetBoundary := reset
		if alertBoundary := reset.Add(-ResetApproachingLead); alertBoundary.After(now) || (alertBoundary.Equal(now) && target.Latest.CapturedAt.Before(now)) {
			resetBoundary = alertBoundary
		}
		if resetBoundary.After(now) && resetBoundary.Before(dueAt) {
			dueAt, trigger = resetBoundary, TriggerPeriodicReset
		}
	}
	if dueAt.Before(floorAt) {
		dueAt = floorAt
	}
	return dueAt, trigger
}

func periodicTrigger(target ScheduleTarget) string {
	if target.Active {
		return TriggerPeriodicActive
	}
	return TriggerPeriodicIdle
}

func targetWithoutPersistedAttempt(target ScheduleTarget) ScheduleTarget {
	target.State.NextAttemptAt = time.Time{}
	return target
}

func exponentialBackoff(failures int, minimum time.Duration) time.Duration {
	if failures < 1 {
		failures = 1
	}
	backoff := minimum
	for count := 1; count < failures && backoff < MaximumBackoff; count++ {
		backoff *= 2
		if backoff > MaximumBackoff {
			backoff = MaximumBackoff
		}
	}
	return backoff
}

func (scheduler *Scheduler) boundedJitter(interval time.Duration) time.Duration {
	maximum := interval / 10
	value := scheduler.jitter(interval)
	if value < 0 {
		value = -value
	}
	if value > maximum {
		value = maximum
	}
	return value
}
