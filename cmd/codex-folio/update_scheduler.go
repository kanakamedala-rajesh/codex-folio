package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"venkatasudha.com/codex-folio/internal/updates"
)

const updateSchedulerResolution = time.Hour

type updateSchedulerRunner struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func startUpdateScheduler(service *updates.Service) *updateSchedulerRunner {
	ctx, cancel := context.WithCancel(context.Background())
	runner := &updateSchedulerRunner{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(runner.done)
		_, _ = service.CheckAutomatic(ctx)
		ticker := time.NewTicker(updateSchedulerResolution)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = service.CheckAutomatic(ctx)
			}
		}
	}()
	return runner
}

func (runner *updateSchedulerRunner) Close() error {
	if runner == nil {
		return nil
	}
	runner.once.Do(runner.cancel)
	<-runner.done
	return nil
}

type serviceCloserGroup struct {
	closers []serviceCloser
	once    sync.Once
	err     error
}

func (group *serviceCloserGroup) Close() error {
	if group == nil {
		return nil
	}
	group.once.Do(func() {
		for index := len(group.closers) - 1; index >= 0; index-- {
			if group.closers[index] != nil {
				group.err = errors.Join(group.err, group.closers[index].Close())
			}
		}
	})
	return group.err
}
