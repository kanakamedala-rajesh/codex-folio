package main

import (
	"context"
	"errors"
	"io"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configpack"
	"venkatasudha.com/codex-folio/internal/continuation"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
	usagefeature "venkatasudha.com/codex-folio/internal/usage"
)

const usageRefreshTimeout = 10 * time.Second

type launchCommandService struct {
	store              *store.Store
	workflow           *launch.Workflow
	configurationPacks *configpack.Service
	authenticator      profile.Authenticator
	projects           *activity.ProjectService
	usage              *usageCommandService
	continuations      *continuation.Service
}

func (service *launchCommandService) ActiveManagedLaunchCount(ctx context.Context) (int, error) {
	if err := service.workflow.Reconcile(ctx, foregroundProcessInspector{}); err != nil {
		return 0, err
	}
	return service.store.ActiveManagedLaunchCount(ctx)
}

func (service *launchCommandService) PrepareHandoff(ctx context.Context, request launch.PrepareRequest, version, checkpointID, revision string) (launch.Plan, error) {
	if service.continuations == nil || service.authenticator == nil {
		return launch.Plan{}, apperrors.New(apperrors.ContinuationCheckpointInvalid, continuation.ErrHandoffNotReady)
	}
	item, err := service.store.GetProfile(ctx, request.Alias)
	if err != nil {
		return launch.Plan{}, err
	}
	if err := service.workflow.Reconcile(ctx, foregroundProcessInspector{}); err != nil {
		return launch.Plan{}, err
	}
	if _, err := service.continuations.PrepareHandoff(ctx, checkpointID, revision, item.ID); err != nil {
		return launch.Plan{}, err
	}
	item, err = profile.VerifyAuthentication(ctx, service.store, service.authenticator, profile.AuthenticationCheckRequest{
		Alias: request.Alias, Discovery: profile.Discovery{Executable: request.Executable, Version: version}, Stdout: io.Discard, Stderr: io.Discard,
	})
	if err != nil {
		return launch.Plan{}, err
	}
	if item.IdentityHomeOwnership == profile.HomeOwnershipManaged && service.configurationPacks != nil {
		if _, err := service.configurationPacks.Project(ctx, request.Alias); err != nil && !errors.Is(err, configpack.ErrNoAssignment) {
			return launch.Plan{}, err
		}
	}
	if service.usage != nil {
		refreshCtx, cancel := context.WithTimeout(ctx, usageRefreshTimeout)
		_, _ = service.usage.RefreshWithCandidate(refreshCtx, request.Alias, request.Executable, version, usagefeature.TriggerPreLaunch)
		cancel()
	}
	if err := service.workflow.Reconcile(ctx, foregroundProcessInspector{}); err != nil {
		return launch.Plan{}, err
	}
	prepared, err := service.continuations.PrepareHandoff(ctx, checkpointID, revision, item.ID)
	if err != nil {
		return launch.Plan{}, err
	}
	bootSessionID, err := (foregroundProcessInspector{}).BootSessionID()
	if err != nil {
		return launch.Plan{}, apperrors.New(apperrors.LaunchPlanInvalid, err)
	}
	request.WorkingDirectory = prepared.WorkingDirectory
	request.Arguments = []string{prepared.Context}
	request.ProjectID = prepared.ProjectID
	request.CheckpointID = prepared.CheckpointID
	request.CheckpointRevision = prepared.Revision
	request.SourceProfileID = prepared.SourceProfileID
	request.BootSessionID = bootSessionID
	return service.workflow.Prepare(ctx, request)
}

func newLaunchCommandService(stateStore *store.Store, configurationPacks *configpack.Service, authenticator profile.Authenticator, projects *activity.ProjectService, usage *usageCommandService) (*launchCommandService, error) {
	workflow, err := launch.NewWorkflow(launch.WorkflowOptions{Repository: stateStore})
	if err != nil {
		return nil, err
	}
	return &launchCommandService{store: stateStore, workflow: workflow, configurationPacks: configurationPacks, authenticator: authenticator, projects: projects, usage: usage}, nil
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
	if service.usage != nil {
		refreshCtx, cancel := context.WithTimeout(ctx, usageRefreshTimeout)
		_, _ = service.usage.RefreshWithCandidate(refreshCtx, request.Alias, request.Executable, version, usagefeature.TriggerPreLaunch)
		cancel()
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

func (service *launchCommandService) MarkExited(ctx context.Context, leaseID string, exitStatus int, executable, version string) (*launch.SafeContinuationOffer, error) {
	var record launch.ManagedLaunch
	if service.usage != nil {
		var err error
		record, err = service.store.GetManagedLaunch(ctx, leaseID)
		if err != nil {
			return nil, err
		}
	}
	if err := service.workflow.MarkExited(ctx, leaseID, exitStatus); err != nil {
		return nil, err
	}
	if service.usage != nil {
		refreshCtx, cancel := context.WithTimeout(ctx, usageRefreshTimeout)
		snapshot, refreshErr := service.usage.RefreshWithCandidate(refreshCtx, record.ProfileAlias, executable, version, usagefeature.TriggerPostExit)
		cancel()
		if refreshErr != nil || !supportedQuotaCondition(snapshot, record.ProfileID) {
			return nil, nil
		}
		candidates, recommended, err := service.usage.workflow.RankAlternatives(ctx, record.ProfileID, true)
		if err != nil {
			return nil, nil
		}
		offer := &launch.SafeContinuationOffer{Alternatives: []launch.SafeContinuationAlternative{}}
		for _, candidate := range candidates {
			if !candidate.Eligible {
				continue
			}
			offer.Alternatives = append(offer.Alternatives, launch.SafeContinuationAlternative{
				Alias: candidate.Alias, CapacityState: candidate.CapacityState, Provenance: usagefeature.ProvenanceProvider,
				Recommended: candidate.ProfileID == recommended,
			})
		}
		if len(offer.Alternatives) != 0 {
			return offer, nil
		}
	}
	return nil, nil
}

func supportedQuotaCondition(snapshot usagefeature.Snapshot, profileID string) bool {
	if snapshot.ProfileID != profileID || snapshot.TriggerReason != usagefeature.TriggerPostExit || snapshot.Source != usagefeature.SourceCodexAppServer || snapshot.SourceVersion == "" {
		return false
	}
	metrics := make(map[string]usagefeature.Metric)
	for _, metric := range usagefeature.Registry() {
		if metric.SourceClass == usagefeature.ProvenanceProvider {
			metrics[metric.Key] = metric
		}
	}
	for _, observation := range snapshot.Observations {
		metric, supported := metrics[observation.Metric.Key]
		if supported && observation.Metric == metric && observation.Value == 100 && observation.Source == usagefeature.SourceCodexAppServer && observation.SourceVersion != "" && observation.Provenance == usagefeature.ProvenanceProvider && observation.Freshness == usagefeature.FreshnessFresh && observation.Availability == usagefeature.AvailabilityAvailable {
			return true
		}
	}
	return false
}

func (service *launchCommandService) MarkAbandoned(ctx context.Context, leaseID string) error {
	return service.workflow.MarkAbandoned(ctx, leaseID)
}
