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
