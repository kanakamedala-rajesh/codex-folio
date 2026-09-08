package main

import (
	"context"
	"errors"

	"venkatasudha.com/codex-folio/internal/activity"
	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/store"
)

type activityCommandService struct {
	workflow *activity.Service
	resolver launch.ExecutableResolver
	store    *store.Store
}

func newActivityCommandService(stateStore *store.Store, projects *activity.ProjectService, resolver launch.ExecutableResolver) (*activityCommandService, error) {
	workflow, err := activity.NewService(activity.ServiceOptions{Repository: stateStore, Reader: codexadapter.NewLocalActivityReader(), Projects: projects})
	if err != nil {
		return nil, err
	}
	if resolver == nil {
		resolver = codexadapter.NewResolver(codexadapter.ResolverOptions{})
	}
	return &activityCommandService{workflow: workflow, resolver: resolver, store: stateStore}, nil
}

func (service *activityCommandService) Refresh(ctx context.Context, alias string) ([]activity.TimelineRecord, error) {
	candidate, err := service.resolver.Resolve("")
	if err != nil {
		return nil, err
	}
	records, err := service.workflow.Refresh(ctx, alias, candidate.Version)
	if err == nil {
		_, err = service.store.RetainAnalytics(ctx)
	}
	if errors.Is(err, activity.ErrActivityInvalid) {
		return nil, apperrors.New(apperrors.ActivityRequestInvalid, err)
	}
	if errors.Is(err, activity.ErrActivityUnavailable) {
		return nil, apperrors.New(apperrors.ActivitySourceUnavailable, err)
	}
	return records, err
}

func (service *activityCommandService) List(ctx context.Context, filters activity.Filters) ([]activity.TimelineRecord, error) {
	return service.workflow.List(ctx, filters)
}
