package main

import (
	"context"
	"errors"
	"io"

	"venkatasudha.com/codex-folio/internal/configpack"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
)

type launchCommandService struct {
	store              *store.Store
	workflow           *launch.Workflow
	configurationPacks *configpack.Service
	authenticator      profile.Authenticator
}

func newLaunchCommandService(stateStore *store.Store, configurationPacks *configpack.Service, authenticator profile.Authenticator) (*launchCommandService, error) {
	workflow, err := launch.NewWorkflow(launch.WorkflowOptions{Repository: stateStore})
	if err != nil {
		return nil, err
	}
	return &launchCommandService{store: stateStore, workflow: workflow, configurationPacks: configurationPacks, authenticator: authenticator}, nil
}

func (service *launchCommandService) Prepare(ctx context.Context, request launch.PrepareRequest, version string) (launch.Plan, error) {
	item, err := service.store.GetProfile(ctx, request.Alias)
	if err != nil {
		return launch.Plan{}, err
	}
	if service.authenticator != nil && (item.Status == profile.StatusReady || item.Status == profile.StatusNeedsReauthentication || item.Status == profile.StatusUnavailable) {
		item, err = profile.VerifyAuthentication(ctx, service.store, service.authenticator, profile.AuthenticationCheckRequest{
			Alias:     request.Alias,
			Discovery: profile.Discovery{Executable: request.Executable, Version: version},
			Stdout:    io.Discard, Stderr: io.Discard,
		})
		if err != nil {
			return launch.Plan{}, err
		}
	}
	if item.Status == profile.StatusReady && item.IdentityHomeOwnership == profile.HomeOwnershipManaged && service.configurationPacks != nil {
		if _, err := service.configurationPacks.Project(ctx, request.Alias); err != nil && !errors.Is(err, configpack.ErrNoAssignment) {
			return launch.Plan{}, err
		}
	}
	if err := service.workflow.Reconcile(ctx, foregroundProcessInspector{}); err != nil {
		return launch.Plan{}, err
	}
	return service.workflow.Prepare(ctx, request)
}

func (service *launchCommandService) MarkStarted(ctx context.Context, leaseID string, processID int) error {
	return service.workflow.MarkStarted(ctx, leaseID, processID)
}

func (service *launchCommandService) MarkExited(ctx context.Context, leaseID string, exitStatus int) error {
	return service.workflow.MarkExited(ctx, leaseID, exitStatus)
}

func (service *launchCommandService) MarkAbandoned(ctx context.Context, leaseID string) error {
	return service.workflow.MarkAbandoned(ctx, leaseID)
}

func (service *launchCommandService) MarkUnavailable(ctx context.Context, alias string) error {
	item, err := service.store.GetProfile(ctx, alias)
	if errors.Is(err, profile.ErrNotFound) || (err == nil && item.Status == profile.StatusPending) {
		return nil
	}
	if err != nil {
		return err
	}
	return service.store.SetAuthenticationState(ctx, item.ID, profile.StatusUnavailable, "")
}
