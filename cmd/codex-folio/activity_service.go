package main

import (
	"context"
	"errors"

	"venkatasudha.com/codex-folio/internal/activity"
	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/store"
)

type activityCommandService struct {
	workflow *activity.Service
	store    *store.Store
}

func newActivityCommandService(stateStore *store.Store, projects *activity.ProjectService) (*activityCommandService, error) {
	workflow, err := activity.NewService(activity.ServiceOptions{Repository: stateStore, Reader: codexadapter.NewLocalActivityReader(), Projects: projects})
	if err != nil {
		return nil, err
	}
	return &activityCommandService{workflow: workflow, store: stateStore}, nil
}

func (service *activityCommandService) Refresh(ctx context.Context, alias string) ([]activity.TimelineRecord, error) {
	records, err := service.workflow.Refresh(ctx, alias, codexadapter.LocalActivitySourceVersion)
	if err == nil {
		_, err = service.store.RetainAnalytics(ctx)
		if err == nil {
			records, err = service.workflow.List(ctx, activity.Filters{ProfileAlias: alias})
		}
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
