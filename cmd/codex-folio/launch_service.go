package main

import (
	"context"
	"errors"
	"io"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
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
	projects           *activity.ProjectService
}

func newLaunchCommandService(stateStore *store.Store, configurationPacks *configpack.Service, authenticator profile.Authenticator, projects *activity.ProjectService) (*launchCommandService, error) {
	workflow, err := launch.NewWorkflow(launch.WorkflowOptions{Repository: stateStore})
	if err != nil {
		return nil, err
	}
	return &launchCommandService{store: stateStore, workflow: workflow, configurationPacks: configurationPacks, authenticator: authenticator, projects: projects}, nil
}

func (service *launchCommandService) Prepare(ctx context.Context, request launch.PrepareRequest, version string) (launch.Plan, string, error) {
	item, err := service.store.GetProfile(ctx, request.Alias)
	if err != nil {
		return launch.Plan{}, "", err
	}
	authWarning := false
	if service.authenticator != nil && (item.Status == profile.StatusReady || item.Status == profile.StatusNeedsReauthentication || item.Status == profile.StatusUnavailable) {
		verified, verifyErr := profile.VerifyAuthentication(ctx, service.store, service.authenticator, profile.AuthenticationCheckRequest{
			Alias:     request.Alias,
			Discovery: profile.Discovery{Executable: request.Executable, Version: version},
			Stdout:    io.Discard, Stderr: io.Discard,
		})
		if verifyErr == nil {
			item = verified
		} else if apperrors.Code(verifyErr) == apperrors.ProfileAuthenticationUnavailable && item.Status == profile.StatusReady {
			authWarning = true
		} else {
			return launch.Plan{}, "", verifyErr
		}
	}
	if item.Status == profile.StatusReady && item.IdentityHomeOwnership == profile.HomeOwnershipManaged && service.configurationPacks != nil {
		if _, err := service.configurationPacks.Project(ctx, request.Alias); err != nil && !errors.Is(err, configpack.ErrNoAssignment) {
			return launch.Plan{}, "", err
		}
	}
	if err := service.workflow.Reconcile(ctx, foregroundProcessInspector{}); err != nil {
		return launch.Plan{}, "", err
	}
	if service.projects != nil {
		project, projectErr := service.projects.Resolve(ctx, request.WorkingDirectory, "")
		if projectErr != nil && !errors.Is(projectErr, activity.ErrPathInvalid) {
			return launch.Plan{}, "", projectErr
		}
		if projectErr == nil {
			request.ProjectID = project.ID
		}
	}
	plan, err := service.workflow.Prepare(ctx, request)
	if err != nil {
		return launch.Plan{}, "", err
	}
	if authWarning {
		return plan, "Codex authentication metadata is unavailable; continuing with basic launch.", nil
	}
	return plan, "", nil
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
