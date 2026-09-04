package profile

import (
	"context"
	"errors"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
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

func TestAddReferencedHomeReusesUsableCodexAuthentication(t *testing.T) {
	repository := newMemoryRepository()
	authenticator := &recordingAuthenticator{}
	workflow := mustWorkflow(t, repository, authenticator)

	result, err := workflow.Add(context.Background(), SetupRequest{
		Alias:              "external",
		ReferencedHomePath: "/external/codex-home",
		NonInteractive:     true,
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if result.Profile.Status != StatusReady || !result.Profile.Selected || result.Profile.IdentityHomeOwnership != HomeOwnershipReferenced {
		t.Fatalf("profile = %#v, want ready selected referenced profile", result.Profile)
	}
	if result.AuthenticationMethod != AuthMethodReused || authenticator.authenticateCalls != 0 || authenticator.checkCalls != 1 {
		t.Fatalf("authentication = method:%q authenticate:%d check:%d, want reused, 0, 1", result.AuthenticationMethod, authenticator.authenticateCalls, authenticator.checkCalls)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want no warning without documented metadata", result.Warnings)
	}
}

func TestAddWarnsOnlyWhenDocumentedMetadataMatchesAnotherProfile(t *testing.T) {
	for _, test := range []struct {
		name      string
		metadata  DocumentedMetadata
		duplicate bool
	}{
		{name: "login identity", metadata: DocumentedMetadata{LoginIdentity: "login-1"}, duplicate: true},
		{name: "workspace", metadata: DocumentedMetadata{Workspace: "workspace-1"}, duplicate: true},
		{name: "distinct profile", metadata: DocumentedMetadata{LoginIdentity: "login-2", Workspace: "workspace-2"}},
		{name: "no metadata"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := newMemoryRepository()
			repository.documented = []DocumentedMetadata{{LoginIdentity: "login-1", Workspace: "workspace-1"}}
			result, err := mustWorkflowWithID(t, repository, &recordingAuthenticator{metadata: test.metadata}, "profile-2").Add(context.Background(), SetupRequest{Alias: "profile"})
			if err != nil {
				t.Fatalf("Add() error = %v", err)
			}
			if (len(result.Warnings) == 1) != test.duplicate {
				t.Fatalf("warnings = %#v, duplicate = %v", result.Warnings, test.duplicate)
			}
			if result.Profile.Status != StatusReady {
				t.Fatalf("profile status = %q, want ready", result.Profile.Status)
			}
		})
	}
}

func TestAddReferencedHomeDelegatesReauthenticationWhenCheckFails(t *testing.T) {
	repository := newMemoryRepository()
	authenticator := &recordingAuthenticator{checkErr: ErrNotAuthenticated}
	workflow := mustWorkflow(t, repository, authenticator)

	result, err := workflow.Add(context.Background(), SetupRequest{
		Alias:              "reauth",
		ReferencedHomePath: "/external/codex-home",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if result.AuthenticationMethod != AuthMethodBrowser || authenticator.authenticateCalls != 1 || authenticator.checkCalls != 2 {
		t.Fatalf("authentication = method:%q authenticate:%d check:%d, want browser, 1, 2", result.AuthenticationMethod, authenticator.authenticateCalls, authenticator.checkCalls)
	}
}

func TestAddReferencedHomeKeepsValidationFailurePending(t *testing.T) {
	repository := newMemoryRepository()
	authenticator := &recordingAuthenticator{checkErr: ErrNotAuthenticated, authenticateErr: ErrAuthCancelled}
	workflow := mustWorkflow(t, repository, authenticator)

	_, err := workflow.Add(context.Background(), SetupRequest{
		Alias:              "cancelled",
		ReferencedHomePath: "/external/codex-home",
	})
	if err == nil || !errors.Is(err, ErrAuthCancelled) {
		t.Fatalf("Add() error = %v, want authentication cancellation", err)
	}
	if repository.ready || repository.pending.Status != StatusPending || repository.pending.IdentityHomeOwnership != HomeOwnershipReferenced || !repository.pending.Stages.Discovery || !repository.pending.Stages.Home || repository.pending.Stages.Authentication {
		t.Fatalf("pending = %#v, want referenced home-only pending profile", repository.pending)
	}
}

func TestReauthenticateUsesSavedDevicePreferenceAndExistingHome(t *testing.T) {
	homePath := t.TempDir()
	repository := &authenticationRepositoryStub{profile: IdentityProfile{
		ID:                    "profile-1",
		Alias:                 "work",
		Status:                StatusNeedsReauthentication,
		IdentityHomeID:        "home-1",
		IdentityHomeOwnership: HomeOwnershipManaged,
		IdentityHomePath:      homePath,
		AuthenticationMethod:  AuthMethodDeviceCode,
	}}
	authenticator := &recordingAuthenticator{checkErr: ErrNotAuthenticated}

	result, err := Reauthenticate(context.Background(), repository, recordingDiscoverer{}, authenticator, ReauthenticationRequest{Alias: "WORK"})
	if err != nil {
		t.Fatalf("Reauthenticate() error = %v", err)
	}
	if !result.Reauthenticated || result.Profile.Status != StatusReady || result.AuthenticationMethod != AuthMethodDeviceCode {
		t.Fatalf("result = %#v, want successful device-code recovery", result)
	}
	if authenticator.methods[len(authenticator.methods)-1] != AuthMethodDeviceCode || authenticator.identityHome != homePath {
		t.Fatalf("authentication = methods:%v home:%q, want device-code and existing home", authenticator.methods, authenticator.identityHome)
	}
	if repository.status != StatusReady || repository.method != AuthMethodDeviceCode {
		t.Fatalf("saved state = status:%q method:%q, want ready/device-code", repository.status, repository.method)
	}
}

func TestVerifyAuthenticationStopsLaunchAndMarksProfileForReauthentication(t *testing.T) {
	homePath := t.TempDir()
	repository := &authenticationRepositoryStub{profile: IdentityProfile{
		ID:               "profile-1",
		Alias:            "work",
		Status:           StatusReady,
		IdentityHomeID:   "home-1",
		IdentityHomePath: homePath,
	}}
	authenticator := &recordingAuthenticator{checkErr: ErrNotAuthenticated}

	item, err := VerifyAuthentication(context.Background(), repository, authenticator, AuthenticationCheckRequest{
		Alias:     "work",
		Discovery: Discovery{Executable: "/opt/codex/bin/codex"},
	})
	if err == nil || !errors.Is(err, ErrReauthenticationRequired) {
		t.Fatalf("VerifyAuthentication() error = %v, want reauthentication required", err)
	}
	if apperrors.Code(err) != apperrors.ProfileReauthenticationRequired || item.Status != StatusNeedsReauthentication || repository.status != StatusNeedsReauthentication {
		t.Fatalf("result/error/state = %#v/%v/%q, want coded needs-reauthentication state", item, err, repository.status)
	}
}

func TestVerifyAuthenticationMarksStatusInterfaceFailureUnavailable(t *testing.T) {
	homePath := t.TempDir()
	repository := &authenticationRepositoryStub{profile: IdentityProfile{
		ID:               "profile-1",
		Alias:            "work",
		Status:           StatusReady,
		IdentityHomeID:   "home-1",
		IdentityHomePath: homePath,
	}}

	item, err := VerifyAuthentication(context.Background(), repository, &recordingAuthenticator{checkErr: ErrAuthenticationUnavailable}, AuthenticationCheckRequest{
		Alias:     "work",
		Discovery: Discovery{Executable: "/opt/codex/bin/codex"},
	})
	if err == nil || apperrors.Code(err) != apperrors.ProfileAuthenticationUnavailable {
		t.Fatalf("VerifyAuthentication() error = %v, want authentication unavailable", err)
	}
	if item.Status != StatusUnavailable || repository.status != StatusUnavailable {
		t.Fatalf("result/state = %q/%q, want unavailable", item.Status, repository.status)
	}
}

func TestReauthenticateReusesUsableAuthenticationWithoutLogin(t *testing.T) {
	homePath := t.TempDir()
	repository := &authenticationRepositoryStub{profile: IdentityProfile{
		ID:                   "profile-1",
		Alias:                "work",
		Status:               StatusNeedsReauthentication,
		IdentityHomeID:       "home-1",
		IdentityHomePath:     homePath,
		AuthenticationMethod: AuthMethodBrowser,
	}}
	authenticator := &recordingAuthenticator{}

	result, err := Reauthenticate(context.Background(), repository, recordingDiscoverer{}, authenticator, ReauthenticationRequest{Alias: "work"})
	if err != nil || result.Reauthenticated || result.AuthenticationMethod != AuthMethodReused {
		t.Fatalf("result/error = %#v/%v, want reused authentication without reauth", result, err)
	}
	if authenticator.authenticateCalls != 0 || repository.status != StatusReady {
		t.Fatalf("authentication/state = %d/%q, want no login and ready state", authenticator.authenticateCalls, repository.status)
	}
}

func TestReauthenticateNonInteractiveRequiresSavedOrExplicitMethod(t *testing.T) {
	homePath := t.TempDir()
	repository := &authenticationRepositoryStub{profile: IdentityProfile{
		ID:               "profile-1",
		Alias:            "work",
		Status:           StatusReady,
		IdentityHomeID:   "home-1",
		IdentityHomePath: homePath,
	}}
	authenticator := &recordingAuthenticator{checkErr: ErrNotAuthenticated}

	_, err := Reauthenticate(context.Background(), repository, recordingDiscoverer{}, authenticator, ReauthenticationRequest{Alias: "work", NonInteractive: true})
	if err == nil || apperrors.Code(err) != apperrors.ProfileSetupChoiceRequired || !errors.Is(err, ErrSetupChoiceRequired) {
		t.Fatalf("Reauthenticate() error = %v, want precise non-interactive choice guidance", err)
	}
	if repository.status != StatusNeedsReauthentication || authenticator.authenticateCalls != 0 {
		t.Fatalf("state/authentication = %q/%d, want needs-reauthentication without prompt", repository.status, authenticator.authenticateCalls)
	}
}

func TestReauthenticateCancellationLeavesProfileUnlaunchable(t *testing.T) {
	homePath := t.TempDir()
	repository := &authenticationRepositoryStub{profile: IdentityProfile{
		ID:                   "profile-1",
		Alias:                "work",
		Status:               StatusNeedsReauthentication,
		IdentityHomeID:       "home-1",
		IdentityHomePath:     homePath,
		AuthenticationMethod: AuthMethodDeviceCode,
	}}
	authenticator := &recordingAuthenticator{checkErr: ErrNotAuthenticated, authenticateErr: ErrAuthCancelled}

	_, err := Reauthenticate(context.Background(), repository, recordingDiscoverer{}, authenticator, ReauthenticationRequest{Alias: "work"})
	if err == nil || apperrors.Code(err) != apperrors.ProfileAuthenticationCancelled || !errors.Is(err, ErrAuthCancelled) {
		t.Fatalf("Reauthenticate() error = %v, want cancellation", err)
	}
	if repository.status != StatusNeedsReauthentication {
		t.Fatalf("profile state = %q, want needs reauthentication after cancellation", repository.status)
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

func TestRegistryEditsProfileMetadataThroughStableIdentity(t *testing.T) {
	repository := &registryRepositoryStub{profiles: []IdentityProfile{{
		ID: "profile-1", Alias: "Work", DisplayName: "Work", Status: StatusReady,
		IdentityHomeID: "home-1", IdentityHomeOwnership: HomeOwnershipManaged,
	}}}
	registry, err := NewRegistry(repository)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	newAlias, displayName, email, workspace := "client", "Client work", "user@example.com", "Example"
	updated, err := registry.Edit(context.Background(), "WORK", ProfileEdits{
		Alias: &newAlias, DisplayName: &displayName, Email: &email, Workspace: &workspace,
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if updated.ID != "profile-1" || updated.IdentityHomeID != "home-1" || updated.IdentityHomeOwnership != HomeOwnershipManaged {
		t.Fatalf("updated identity = %#v, want stable profile and home identity", updated)
	}
	if updated.Alias != "client" || updated.DisplayName != "Client work" || updated.Email != email || updated.Workspace != workspace {
		t.Fatalf("updated metadata = %#v", updated)
	}
	if repository.editedAlias != "WORK" {
		t.Fatalf("repository edit alias = %q, want original command alias", repository.editedAlias)
	}
}

func TestRegistryRejectsInvalidAliasBeforeEditing(t *testing.T) {
	repository := &registryRepositoryStub{}
	registry, err := NewRegistry(repository)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	invalid := "../work"
	if _, err := registry.Edit(context.Background(), "work", ProfileEdits{Alias: &invalid}); !errors.Is(err, ErrAliasInvalid) {
		t.Fatalf("Edit() error = %v, want invalid alias", err)
	}
	if repository.editedAlias != "" {
		t.Fatal("invalid edit reached repository")
	}
}

type registryRepositoryStub struct {
	profiles    []IdentityProfile
	editedAlias string
}

func (repository *registryRepositoryStub) ListProfiles(context.Context) ([]IdentityProfile, error) {
	return append([]IdentityProfile(nil), repository.profiles...), nil
}

func (repository *registryRepositoryStub) EditProfile(_ context.Context, alias string, edits ProfileEdits) (IdentityProfile, error) {
	repository.editedAlias = alias
	item := repository.profiles[0]
	if edits.Alias != nil {
		item.Alias = *edits.Alias
	}
	if edits.DisplayName != nil {
		item.DisplayName = *edits.DisplayName
	}
	if edits.Email != nil {
		item.Email = *edits.Email
	}
	if edits.Workspace != nil {
		item.Workspace = *edits.Workspace
	}
	repository.profiles[0] = item
	return item, nil
}

type recordingDiscoverer struct{}

func (recordingDiscoverer) Discover(string) (Discovery, error) {
	return Discovery{Executable: "/opt/codex/bin/codex", Version: "0.1.2"}, nil
}

type recordingAuthenticator struct {
	authenticateErr   error
	browserErr        error
	checkErr          error
	metadata          DocumentedMetadata
	identityHome      string
	methods           []AuthMethod
	authenticateCalls int
	checkCalls        int
}

type authenticationRepositoryStub struct {
	profile IdentityProfile
	status  Status
	method  AuthMethod
}

func (repository *authenticationRepositoryStub) GetProfile(context.Context, string) (IdentityProfile, error) {
	return repository.profile, nil
}

func (repository *authenticationRepositoryStub) SetAuthenticationState(_ context.Context, profileID string, status Status, method AuthMethod) error {
	if profileID != repository.profile.ID {
		return ErrNotFound
	}
	repository.status = status
	if method != "" {
		repository.method = method
	}
	return nil
}

func (auth *recordingAuthenticator) ObserveDocumentedMetadata(context.Context, AuthenticationRequest) (DocumentedMetadata, error) {
	return auth.metadata, nil
}

func (auth *recordingAuthenticator) Authenticate(_ context.Context, request AuthenticationRequest) error {
	auth.authenticateCalls++
	auth.identityHome = request.IdentityHome
	auth.methods = append(auth.methods, request.Method)
	if request.Method == AuthMethodBrowser && auth.browserErr != nil {
		return auth.browserErr
	}
	return auth.authenticateErr
}

func (auth *recordingAuthenticator) Check(context.Context, AuthenticationRequest) error {
	auth.checkCalls++
	err := auth.checkErr
	auth.checkErr = nil
	return err
}

type memoryRepository struct {
	pending     PendingProfile
	created     bool
	createCalls int
	ready       bool
	selected    bool
	documented  []DocumentedMetadata
}

func newMemoryRepository() *memoryRepository { return &memoryRepository{} }

func (repository *memoryRepository) SaveDocumentedMetadata(_ context.Context, profileID string, metadata DocumentedMetadata) (bool, error) {
	if repository.pending.ID != profileID {
		return false, ErrNotFound
	}
	duplicate := false
	for _, existing := range repository.documented {
		if (metadata.LoginIdentity != "" && metadata.LoginIdentity == existing.LoginIdentity) || (metadata.Workspace != "" && metadata.Workspace == existing.Workspace) {
			duplicate = true
		}
	}
	repository.documented = append(repository.documented, metadata)
	return duplicate, nil
}

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
	repository.pending.IdentityHomeOwnership = HomeOwnershipManaged
	repository.pending.IdentityHomeID = homeID
	repository.pending.IdentityHomePath = homePath
	return nil
}

func (repository *memoryRepository) SetReferencedHome(_ context.Context, profileID, homeID, homePath string) error {
	if repository.pending.ID != profileID {
		return ErrNotFound
	}
	repository.pending.IdentityHomeOwnership = HomeOwnershipReferenced
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
		IdentityHomeOwnership: repository.pending.IdentityHomeOwnership,
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
		IdentityHomeOwnership: repository.pending.IdentityHomeOwnership,
		Selected:              true,
	}, nil
}

type recordingHomeProvisioner struct{}

func (recordingHomeProvisioner) Ensure(_ context.Context, profileID string) (string, error) {
	return "/private/managed-homes/" + profileID, nil
}

type recordingReferencedHomeResolver struct{}

func (recordingReferencedHomeResolver) Resolve(_ context.Context, _ string) (string, error) {
	return "/private/referenced-home", nil
}

func mustWorkflow(t *testing.T, repository Repository, authenticator Authenticator) *Workflow {
	return mustWorkflowWithID(t, repository, authenticator, "profile-1")
}

func mustWorkflowWithID(t *testing.T, repository Repository, authenticator Authenticator, id string) *Workflow {
	t.Helper()
	workflow, err := NewWorkflow(WorkflowOptions{
		Repository:             repository,
		Discoverer:             recordingDiscoverer{},
		HomeProvisioner:        recordingHomeProvisioner{},
		ReferencedHomeResolver: recordingReferencedHomeResolver{},
		Authenticator:          authenticator,
		IDGenerator:            func() (string, error) { return id, nil },
	})
	if err != nil {
		t.Fatalf("NewWorkflow() error = %v", err)
	}
	return workflow
}
