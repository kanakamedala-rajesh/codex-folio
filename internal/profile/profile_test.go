package profile

import (
	"context"
	"errors"
	"testing"
)

func TestAddProfilePromotesOnlyAfterAuthenticationAndHomeValidation(t *testing.T) {
	repository := newMemoryRepository()
	authenticator := &recordingAuthenticator{}
	workflow := mustWorkflow(t, repository, authenticator)

	result, err := workflow.Add(context.Background(), SetupRequest{
		Alias: "Work",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if result.Profile.Status != StatusReady || !result.Profile.Selected {
		t.Fatalf("profile = %#v, want ready and selected", result.Profile)
	}
	if result.Profile.Alias != "Work" || result.Profile.IdentityHomeOwnership != HomeOwnershipManaged {
		t.Fatalf("profile = %#v, want alias and managed ownership", result.Profile)
	}
	if !result.Stages.Complete() {
		t.Fatalf("stages = %#v, want complete", result.Stages)
	}
	if authenticator.authenticateCalls != 1 || authenticator.checkCalls != 1 {
		t.Fatalf("authentication calls = authenticate:%d check:%d, want 1 each", authenticator.authenticateCalls, authenticator.checkCalls)
	}
	if repository.pending.Status != StatusReady {
		t.Fatalf("pending status = %q, want ready before selection cleanup", repository.pending.Status)
	}
}

func TestAddProfileLeavesPendingAndResumesAfterAuthenticationFailure(t *testing.T) {
	repository := newMemoryRepository()
	authenticator := &recordingAuthenticator{authenticateErr: ErrAuthCancelled}
	workflow := mustWorkflow(t, repository, authenticator)

	_, err := workflow.Add(context.Background(), SetupRequest{
		Alias: "personal",
	})
	if err == nil || !errors.Is(err, ErrAuthCancelled) {
		t.Fatalf("first Add() error = %v, want authentication cancellation", err)
	}
	if repository.pending.Status != StatusPending || repository.pending.Stages.Discovery != true || repository.pending.Stages.Home != true || repository.pending.Stages.Authentication || repository.pending.Stages.Validation {
		t.Fatalf("pending after failure = %#v, want discovered/home-only pending state", repository.pending)
	}
	if repository.ready {
		t.Fatal("failed authentication promoted a ready profile")
	}

	authenticator.authenticateErr = nil
	result, err := workflow.Add(context.Background(), SetupRequest{
		Alias: "PERSONAL",
	})
	if err != nil {
		t.Fatalf("resumed Add() error = %v", err)
	}
	if !result.Resumed || result.Profile.Status != StatusReady || !result.Profile.Selected {
		t.Fatalf("resumed result = %#v, want resumed ready selected profile", result)
	}
	if repository.createCalls != 1 {
		t.Fatalf("CreatePendingProfile calls = %d, want one stable pending registration", repository.createCalls)
	}
}

func TestAddProfileUsesDeviceCodeWhenBrowserIsUnavailable(t *testing.T) {
	repository := newMemoryRepository()
	authenticator := &recordingAuthenticator{browserErr: ErrBrowserUnavailable}
	workflow := mustWorkflow(t, repository, authenticator)

	result, err := workflow.Add(context.Background(), SetupRequest{Alias: "device"})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if authenticator.methods[0] != AuthMethodBrowser || authenticator.methods[1] != AuthMethodDeviceCode {
		t.Fatalf("authentication methods = %v, want browser then device-code", authenticator.methods)
	}
	if result.AuthenticationMethod != AuthMethodDeviceCode {
		t.Fatalf("authentication method = %q, want device-code", result.AuthenticationMethod)
	}
}

func TestAddProfileRequiresExplicitAuthenticationMethodNonInteractive(t *testing.T) {
	workflow := mustWorkflow(t, newMemoryRepository(), &recordingAuthenticator{})

	_, err := workflow.Add(context.Background(), SetupRequest{
		Alias:          "automation",
		NonInteractive: true,
	})
	if err == nil || !errors.Is(err, ErrSetupChoiceRequired) {
		t.Fatalf("Add() error = %v, want missing-choice error", err)
	}
}

func TestValidateAliasUsesPortableCaseInsensitiveRules(t *testing.T) {
	for _, alias := range []string{"work", "Work_Profile", "personal-2", "a.b"} {
		if err := ValidateAlias(alias); err != nil {
			t.Errorf("ValidateAlias(%q) error = %v", alias, err)
		}
	}
	for _, alias := range []string{"", " work", "work profile", "../work", "ümlaut", "-work"} {
		if err := ValidateAlias(alias); err == nil {
			t.Errorf("ValidateAlias(%q) succeeded, want invalid alias", alias)
		}
	}
}

type recordingDiscoverer struct{}

func (recordingDiscoverer) Discover(string) (Discovery, error) {
	return Discovery{Executable: "/opt/codex/bin/codex", Version: "0.1.2"}, nil
}

type recordingAuthenticator struct {
	authenticateErr   error
	browserErr        error
	methods           []AuthMethod
	authenticateCalls int
	checkCalls        int
}

func (auth *recordingAuthenticator) Authenticate(_ context.Context, request AuthenticationRequest) error {
	auth.authenticateCalls++
	auth.methods = append(auth.methods, request.Method)
	if request.Method == AuthMethodBrowser && auth.browserErr != nil {
		return auth.browserErr
	}
	return auth.authenticateErr
}

func (auth *recordingAuthenticator) Check(context.Context, AuthenticationRequest) error {
	auth.checkCalls++
	return nil
}

type memoryRepository struct {
	pending     PendingProfile
	created     bool
	createCalls int
	ready       bool
	selected    bool
}

func newMemoryRepository() *memoryRepository { return &memoryRepository{} }

func (repository *memoryRepository) FindPendingProfile(_ context.Context, alias string) (PendingProfile, error) {
	if !repository.created || !equalAlias(repository.pending.Alias, alias) {
		return PendingProfile{}, ErrNotFound
	}
	return repository.pending, nil
}

func (repository *memoryRepository) GetPendingProfile(context.Context, string) (PendingProfile, error) {
	if !repository.created {
		return PendingProfile{}, ErrNotFound
	}
	return repository.pending, nil
}

func (repository *memoryRepository) CreatePendingProfile(_ context.Context, pending PendingProfile) error {
	if repository.created {
		return ErrAliasTaken
	}
	repository.pending = pending
	repository.created = true
	repository.createCalls++
	return nil
}

func (repository *memoryRepository) SetManagedHome(_ context.Context, profileID, homeID, homePath string) error {
	if repository.pending.ID != profileID {
		return ErrNotFound
	}
	repository.pending.IdentityHomeID = homeID
	repository.pending.IdentityHomePath = homePath
	return nil
}

func (repository *memoryRepository) SaveSetupStage(_ context.Context, profileID string, stage SetupStage) error {
	if repository.pending.ID != profileID {
		return ErrNotFound
	}
	switch stage {
	case StageDiscovery:
		repository.pending.Stages.Discovery = true
	case StageHome:
		repository.pending.Stages.Home = true
	case StageAuthentication:
		repository.pending.Stages.Authentication = true
	case StageValidation:
		repository.pending.Stages.Validation = true
	case StageSelection:
		repository.pending.Stages.Selection = true
	default:
		return errors.New("unknown stage")
	}
	return nil
}

func (repository *memoryRepository) ResetAuthentication(_ context.Context, profileID string) error {
	if repository.pending.ID != profileID {
		return ErrNotFound
	}
	repository.pending.Stages.Authentication = false
	repository.pending.Stages.Validation = false
	repository.pending.Status = StatusPending
	return nil
}

func (repository *memoryRepository) PromotePendingProfile(_ context.Context, profileID string) (IdentityProfile, error) {
	if repository.pending.ID != profileID || !repository.pending.Stages.Authentication || !repository.pending.Stages.Validation {
		return IdentityProfile{}, ErrNotFound
	}
	repository.ready = true
	repository.pending.Status = StatusReady
	return IdentityProfile{
		ID:                    repository.pending.ID,
		Alias:                 repository.pending.Alias,
		DisplayName:           repository.pending.DisplayName,
		Status:                StatusReady,
		IdentityHomeID:        repository.pending.IdentityHomeID,
		IdentityHomeOwnership: HomeOwnershipManaged,
	}, nil
}

func (repository *memoryRepository) CompleteInitialSelection(_ context.Context, profileID string) (IdentityProfile, error) {
	if !repository.ready || repository.pending.ID != profileID {
		return IdentityProfile{}, ErrNotFound
	}
	repository.pending.Stages.Selection = true
	repository.selected = true
	return IdentityProfile{
		ID:                    repository.pending.ID,
		Alias:                 repository.pending.Alias,
		DisplayName:           repository.pending.DisplayName,
		Status:                StatusReady,
		IdentityHomeID:        repository.pending.IdentityHomeID,
		IdentityHomeOwnership: HomeOwnershipManaged,
		Selected:              true,
	}, nil
}

type recordingHomeProvisioner struct{}

func (recordingHomeProvisioner) Ensure(_ context.Context, profileID string) (string, error) {
	return "/private/managed-homes/" + profileID, nil
}

func mustWorkflow(t *testing.T, repository Repository, authenticator Authenticator) *Workflow {
	t.Helper()
	workflow, err := NewWorkflow(WorkflowOptions{
		Repository:      repository,
		Discoverer:      recordingDiscoverer{},
		HomeProvisioner: recordingHomeProvisioner{},
		Authenticator:   authenticator,
		IDGenerator:     func() (string, error) { return "profile-1", nil },
	})
	if err != nil {
		t.Fatalf("NewWorkflow() error = %v", err)
	}
	return workflow
}
