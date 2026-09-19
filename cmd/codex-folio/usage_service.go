package main

import (
	"context"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	alertfeature "venkatasudha.com/codex-folio/internal/alerts"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/store"
	usagefeature "venkatasudha.com/codex-folio/internal/usage"
)

type usageCommandService struct {
	workflow *usagefeature.Service
	resolver launch.ExecutableResolver
	store    *store.Store
	alerts   *alertfeature.Service
}

type collectionSettingsCommandService struct {
	store   *store.Store
	enabled bool
}

func (service *collectionSettingsCommandService) CollectionSettings(ctx context.Context) (usagefeature.CollectionSettings, bool, error) {
	settings, err := service.store.CollectionSettings(ctx)
	return settings, service.enabled, err
}

func (service *collectionSettingsCommandService) SetCollectionSettings(ctx context.Context, settings usagefeature.CollectionSettings) (usagefeature.CollectionSettings, bool, error) {
	result, err := service.store.SetCollectionSettings(ctx, settings)
	return result, service.enabled, err
}

func newUsageCommandService(stateStore *store.Store, resolver launch.ExecutableResolver) (*usageCommandService, error) {
	workflow, err := usagefeature.NewService(stateStore, codexadapter.NewUsageCollector(), usageClock{})
	if err != nil {
		return nil, err
	}
	if resolver == nil {
		resolver = codexadapter.NewResolver(codexadapter.ResolverOptions{})
	}
	return &usageCommandService{workflow: workflow, resolver: resolver, store: stateStore}, nil
}

func (service *usageCommandService) Refresh(ctx context.Context, alias, triggerReason string) (usagefeature.Snapshot, error) {
	candidate, err := service.resolver.Resolve("")
	if err != nil {
		return usagefeature.Snapshot{}, err
	}
	return service.RefreshWithCandidate(ctx, alias, candidate.Path, candidate.Version, triggerReason)
}

func (service *usageCommandService) RefreshWithCandidate(ctx context.Context, alias, executable, version, triggerReason string) (usagefeature.Snapshot, error) {
	snapshot, err := service.workflow.Refresh(ctx, alias, executable, version, triggerReason)
	if snapshot.ID != "" {
		_, retentionErr := service.store.RetainAnalytics(ctx)
		if err == nil {
			err = retentionErr
		}
	}
	if service.alerts != nil && snapshot.ProfileID != "" {
		_, alertErr := service.alerts.Evaluate(ctx, snapshot.ProfileID)
		if err == nil {
			err = alertErr
		}
	}
	return snapshot, err
}

func (service *usageCommandService) Latest(ctx context.Context, alias string) (usagefeature.Snapshot, error) {
	return service.workflow.Latest(ctx, alias)
}

func (service *usageCommandService) View(ctx context.Context, scope string) (usagefeature.DashboardView, []activity.TimelineRecord, error) {
	if service == nil || service.workflow == nil || service.store == nil {
		return usagefeature.DashboardView{}, nil, usagefeature.ErrInvalid
	}
	discovery, discoveryErr := launch.Discover(service.resolver, "")
	view, err := service.workflow.View(ctx, scope, discoveryErr == nil && discovery.Capabilities.TransparentLaunch == launch.CapabilitySupported)
	if err != nil {
		return usagefeature.DashboardView{}, nil, err
	}
	filters := activity.Filters{}
	if view.Scope == usagefeature.ScopeSelectedProfile && len(view.Candidates) == 1 {
		filters.ProfileAlias = view.Candidates[0].Alias
	}
	records, err := service.store.ListActivity(ctx, filters)
	return view, records, err
}

type usageClock struct{}

func (usageClock) Now() time.Time { return time.Now() }

func (service *usageCommandService) Recent(ctx context.Context, target usagefeature.ProfileTarget) ([]usagefeature.Snapshot, error) {
	return service.store.RecentUsageSnapshots(ctx, target)
}
