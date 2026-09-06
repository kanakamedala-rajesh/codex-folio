package main

import (
	"context"
	"io"

	"venkatasudha.com/codex-folio/internal/configpack"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
)

type profileAuthenticationCommandService struct {
	workflow      *profile.Workflow
	repository    *store.Store
	discoverer    profile.Discoverer
	authenticator profile.Authenticator
	packs         *configpack.Service
}

func newProfileAuthenticationCommandService(paths platform.Paths, repository *store.Store, packs *configpack.Service, resolver launch.ExecutableResolver, authenticator profile.Authenticator) (*profileAuthenticationCommandService, error) {
	homes, err := platform.NewManagedHomeProvisioner(paths.ManagedHomes)
	if err != nil {
		return nil, err
	}
	discoverer := profileDiscoverer{resolver: resolver}
	workflow, err := profile.NewWorkflow(profile.WorkflowOptions{
		Repository: repository, Discoverer: discoverer, HomeProvisioner: homes,
		ReferencedHomeResolver: platform.NewReferencedHomeResolver(paths.ManagedHomes, paths.ProfileQuarantine),
		Authenticator:          authenticator,
	})
	if err != nil {
		return nil, err
	}
	return &profileAuthenticationCommandService{workflow: workflow, repository: repository, discoverer: discoverer, authenticator: authenticator, packs: packs}, nil
}

func (service *profileAuthenticationCommandService) Authenticate(ctx context.Context, request httpapi.CommandProfileAuthenticationRequest, output io.Writer) (httpapi.CommandProfileAuthenticationResult, error) {
	switch request.Action {
	case "add":
		if request.ConfigurationPackID != "" {
			if err := service.packs.ValidateAssignment(ctx, request.ConfigurationPackID, request.ConfigurationPackVersion); err != nil {
				return httpapi.CommandProfileAuthenticationResult{}, err
			}
		}
		result, err := service.workflow.Add(ctx, profile.SetupRequest{
			Alias: request.Alias, DisplayName: request.DisplayName, CodexOverride: request.CodexOverride,
			ReferencedHomePath: request.ReferencedHomePath, AuthMethod: request.AuthMethod, NonInteractive: request.NonInteractive,
			Stdout: output, Stderr: output, ConfigurationPackID: request.ConfigurationPackID, ConfigurationPackVersion: request.ConfigurationPackVersion,
		})
		return httpapi.CommandProfileAuthenticationResult{Setup: &result}, err
	case "reauthenticate":
		result, err := profile.Reauthenticate(ctx, service.repository, service.discoverer, service.authenticator, profile.ReauthenticationRequest{
			Alias: request.Alias, CodexOverride: request.CodexOverride, AuthMethod: request.AuthMethod,
			NonInteractive: request.NonInteractive, Stdout: output, Stderr: output,
		})
		return httpapi.CommandProfileAuthenticationResult{Reauthentication: &result}, err
	default:
		return httpapi.CommandProfileAuthenticationResult{}, profile.ErrProfileStateInvalid
	}
}
