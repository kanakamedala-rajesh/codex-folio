package main

import (
	"context"
	"time"

	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/store"
	usagefeature "venkatasudha.com/codex-folio/internal/usage"
)

type usageCommandService struct {
	workflow *usagefeature.Service
	resolver launch.ExecutableResolver
}

func newUsageCommandService(stateStore *store.Store, resolver launch.ExecutableResolver) (*usageCommandService, error) {
	workflow, err := usagefeature.NewService(stateStore, codexadapter.NewUsageCollector(), usageClock{})
	if err != nil {
		return nil, err
	}
	if resolver == nil {
		resolver = codexadapter.NewResolver(codexadapter.ResolverOptions{})
	}
	return &usageCommandService{workflow: workflow, resolver: resolver}, nil
}

func (service *usageCommandService) Refresh(ctx context.Context, alias string) (usagefeature.Snapshot, error) {
	candidate, err := service.resolver.Resolve("")
	if err != nil {
		return usagefeature.Snapshot{}, err
	}
	return service.workflow.Refresh(ctx, alias, candidate.Path, candidate.Version)
}

func (service *usageCommandService) Latest(ctx context.Context, alias string) (usagefeature.Snapshot, error) {
	return service.workflow.Latest(ctx, alias)
}

type usageClock struct{}

func (usageClock) Now() time.Time { return time.Now() }
