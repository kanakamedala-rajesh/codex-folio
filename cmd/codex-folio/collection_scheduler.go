package main

import (
	"context"
	"crypto/rand"
	"sync"
	"time"

	"venkatasudha.com/codex-folio/internal/usage"
)

const collectionSchedulerResolution = time.Minute

type collectionSchedulerRunner struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// The worker exists for an on-demand companion, but each tick consults the
// durable choice before reading targets or refreshing a profile. A changed
// choice therefore takes effect without restarting the service.
type collectionConsentStore struct {
	usage.ScheduleStore
	consentReader interface {
		CollectionConsent(context.Context) (usage.CollectionConsent, error)
	}
}

type collectionConsentRefresher struct {
	reader interface {
		CollectionConsent(context.Context) (usage.CollectionConsent, error)
	}
	refresher usage.ScheduledRefresher
	gate      *sync.RWMutex
}

func (refresher collectionConsentRefresher) Refresh(ctx context.Context, alias, reason string) (usage.Snapshot, error) {
	if refresher.gate != nil {
		refresher.gate.RLock()
		defer refresher.gate.RUnlock()
	}
	choice, err := refresher.reader.CollectionConsent(ctx)
	if err != nil {
		return usage.Snapshot{}, err
	}
	if choice != usage.CollectionConsentAccepted {
		return usage.Snapshot{}, usage.ErrCollectionNotConsented
	}
	return refresher.refresher.Refresh(ctx, alias, reason)
}

func (repository collectionConsentStore) CollectionScheduleTargets(ctx context.Context) ([]usage.ScheduleTarget, error) {
	consent, err := repository.consentReader.CollectionConsent(ctx)
	if err != nil || consent != usage.CollectionConsentAccepted {
		return nil, err
	}
	return repository.ScheduleStore.CollectionScheduleTargets(ctx)
}

func startCollectionScheduler(scheduler *usage.Scheduler) *collectionSchedulerRunner {
	ctx, cancel := context.WithCancel(context.Background())
	runner := &collectionSchedulerRunner{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(runner.done)
		_, _ = scheduler.Tick(ctx)
		ticker := time.NewTicker(collectionSchedulerResolution)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = scheduler.Tick(ctx)
			}
		}
	}()
	return runner
}

func (runner *collectionSchedulerRunner) Close() error {
	if runner == nil {
		return nil
	}
	runner.once.Do(runner.cancel)
	<-runner.done
	return nil
}

func randomScheduleJitter(interval time.Duration) time.Duration {
	maximum := interval / 10
	if maximum <= 0 {
		return 0
	}
	var sample [1]byte
	if _, err := rand.Read(sample[:]); err != nil {
		return 0
	}
	return time.Duration((int64(maximum) * int64(sample[0])) / 255)
}
