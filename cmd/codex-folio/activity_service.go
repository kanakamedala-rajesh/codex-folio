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
			records, err = service.workflow.List(ctx, activity.Filters{})
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

func (service *activityCommandService) Assign(ctx context.Context, assignment activity.Assignment) error {
	err := service.workflow.Assign(ctx, assignment)
	if errors.Is(err, activity.ErrActivityInvalid) {
		return apperrors.New(apperrors.ActivityRequestInvalid, err)
	}
	return err
}

func (service *activityCommandService) ReviewSources(ctx context.Context) ([]activity.SourceReview, error) {
	return service.workflow.ReviewSources(ctx)
}

func (service *activityCommandService) ImportSource(ctx context.Context, sourceID string, consent bool) (activity.ImportResult, error) {
	result, err := service.workflow.ImportSource(ctx, sourceID, codexadapter.LocalActivitySourceVersion, consent)
	if err == nil {
		_, err = service.store.RetainAnalytics(ctx)
	}
	if errors.Is(err, activity.ErrActivityInvalid) {
		return activity.ImportResult{}, apperrors.New(apperrors.ActivityRequestInvalid, err)
	}
	if errors.Is(err, activity.ErrActivityUnavailable) || errors.Is(err, activity.ErrActivityUnsupportedSource) || errors.Is(err, activity.ErrActivityInvalidSchema) {
		return activity.ImportResult{}, apperrors.New(apperrors.ActivitySourceUnavailable, err)
	}
	return result, err
}
