// Package profile owns Identity Profile onboarding and its fail-closed state
// transitions. It knows nothing about SQLite, the filesystem, or Codex's
// authentication protocol; those boundaries are injected by the composition
// root.
package profile

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

type Status string

const (
	StatusPending               Status = "pending"
	StatusReady                 Status = "ready"
	StatusNeedsReauthentication Status = "needs_reauthentication"
	StatusUnavailable           Status = "unavailable"
)

type HomeOwnership string

const (
	HomeOwnershipManaged    HomeOwnership = "managed"
	HomeOwnershipReferenced HomeOwnership = "referenced"
)

type AuthMethod string

const (
	AuthMethodAutomatic  AuthMethod = "auto"
	AuthMethodBrowser    AuthMethod = "browser"
	AuthMethodDeviceCode AuthMethod = "device-code"
	AuthMethodReused     AuthMethod = "reused"
)

type SetupStage string

const (
	StageDiscovery      SetupStage = "discovery"
	StageHome           SetupStage = "home"
	StageAuthentication SetupStage = "authentication"
	StageValidation     SetupStage = "validation"
	StageSelection      SetupStage = "selection"
)

type SetupStages struct {
	Discovery      bool `json:"discovery"`
	Home           bool `json:"home"`
	Authentication bool `json:"authentication"`
	Validation     bool `json:"validation"`
	Selection      bool `json:"selection"`
}

func (stages SetupStages) Complete() bool {
	return stages.Discovery && stages.Home && stages.Authentication && stages.Validation && stages.Selection
}

type Discovery struct {
	Executable string `json:"executable"`
	Version    string `json:"version"`
}

type IdentityProfile struct {
	ID                    string        `json:"id"`
	Alias                 string        `json:"alias"`
	DisplayName           string        `json:"display_name"`
	Email                 string        `json:"email,omitempty"`
	Workspace             string        `json:"workspace,omitempty"`
	Status                Status        `json:"status"`
	IdentityHomeID        string        `json:"identity_home_id"`
	IdentityHomeOwnership HomeOwnership `json:"identity_home_ownership"`
	AuthenticationMethod  AuthMethod    `json:"authentication_method,omitempty"`
	Selected              bool          `json:"selected"`
	CreatedAt             time.Time     `json:"created_at"`
	UpdatedAt             time.Time     `json:"updated_at"`
	IdentityHomePath      string        `json:"-"`
}

type PendingProfile struct {
	ID                    string        `json:"id"`
	Alias                 string        `json:"alias"`
	DisplayName           string        `json:"display_name"`
	Status                Status        `json:"status"`
	IdentityHomeID        string        `json:"identity_home_id,omitempty"`
	IdentityHomeOwnership HomeOwnership `json:"identity_home_ownership,omitempty"`
	Stages                SetupStages   `json:"stages"`
	IdentityHomePath      string        `json:"-"`
}

type SetupRequest struct {
	Alias              string
	DisplayName        string
	CodexOverride      string
	ReferencedHomePath string
	AuthMethod         AuthMethod
	NonInteractive     bool
	Stdin              io.Reader
	Stdout             io.Writer
	Stderr             io.Writer
}

type SetupResult struct {
	Profile              IdentityProfile `json:"profile"`
	Discovery            Discovery       `json:"discovery"`
	Stages               SetupStages     `json:"stages"`
	Resumed              bool            `json:"resumed"`
	AuthenticationMethod AuthMethod      `json:"authentication_method,omitempty"`
	Warnings             []string        `json:"warnings,omitempty"`
}

type ReauthenticationRequest struct {
	Alias          string
	CodexOverride  string
	AuthMethod     AuthMethod
	NonInteractive bool
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
}

type ReauthenticationResult struct {
	Profile              IdentityProfile `json:"profile"`
	Discovery            Discovery       `json:"discovery"`
	AuthenticationMethod AuthMethod      `json:"authentication_method,omitempty"`
	Reauthenticated      bool            `json:"reauthenticated"`
}

type SelectionResult struct {
	Profile  IdentityProfile `json:"profile"`
	Warnings []string        `json:"warnings,omitempty"`
}

type ProfileEdits struct {
	Alias       *string `json:"alias,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Email       *string `json:"email,omitempty"`
	Workspace   *string `json:"workspace,omitempty"`
}

type InventoryResult struct {
	Profiles []IdentityProfile `json:"profiles"`
}

const RunningLaunchSelectionWarning = "A running Managed Launch keeps its original Launch Profile; this selection applies to the dashboard and next interactive launch."

const ReferencedHomeDuplicateWarning = "Codex-reported Login Identity or Workspace may already be registered; this local profile remains distinct."

var (
	ErrNotFound                      = errors.New("profile was not found")
	ErrAliasInvalid                  = errors.New("profile alias is invalid")
	ErrAliasTaken                    = errors.New("profile alias is already in use")
	ErrSetupChoiceRequired           = errors.New("profile setup requires an explicit choice")
	ErrAuthCancelled                 = errors.New("Codex authentication was cancelled")
	ErrAuthenticationFailed          = errors.New("Codex authentication failed")
	ErrBrowserUnavailable            = errors.New("Codex browser authentication is unavailable")
	ErrNotAuthenticated              = errors.New("Codex authentication is not usable")
	ErrReauthenticationRequired      = errors.New("Codex authentication requires reauthentication")
	ErrAuthenticationUnavailable     = errors.New("Codex authentication status is unavailable")
	ErrDocumentedMetadataUnavailable = errors.New("Codex documented identity metadata is unavailable")
	ErrHomeInvalid                   = errors.New("managed Identity Home is invalid")
	ErrValidationFailed              = errors.New("Identity Home validation failed")
	ErrNotSelectable                 = errors.New("profile is not eligible for selection")
	ErrProfileStateInvalid           = errors.New("profile state is invalid")
	ErrRunningLaunch                 = errors.New("profile has a running Managed Launch")
	ErrReplacementRequired           = errors.New("selected profile requires an eligible replacement")
	ErrQuarantineInvalid             = errors.New("profile quarantine state is invalid")
	ErrQuarantineExpired             = errors.New("profile quarantine has expired")
)

type QuarantineState string

const (
	QuarantinePrepared QuarantineState = "prepared"
	QuarantineReady    QuarantineState = "quarantined"
)

type RemovalAction string

const (
	RemovalQuarantined  RemovalAction = "quarantined"
	RemovalDeregistered RemovalAction = "deregistered"
	RemovalRestored     RemovalAction = "restored"
	RemovalPurged       RemovalAction = "purged"
)

type RemovalRecord struct {
	Profile                IdentityProfile `json:"profile"`
	Action                 RemovalAction   `json:"action"`
	State                  QuarantineState `json:"state,omitempty"`
	QuarantinedAt          time.Time       `json:"quarantined_at,omitempty"`
	PurgeAfter             time.Time       `json:"purge_after,omitempty"`
	RemoteIdentityAffected bool            `json:"remote_identity_affected"`
}

type Discoverer interface {
	Discover(override string) (Discovery, error)
}

type ReferencedHomeResolver interface {
	Resolve(context.Context, string) (string, error)
}

type ManagedHomeProvisioner interface {
	Ensure(context.Context, string) (string, error)
}

type AuthenticationRequest struct {
	Discovery    Discovery
	IdentityHome string
	Method       AuthMethod
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
}

type Authenticator interface {
	Authenticate(context.Context, AuthenticationRequest) error
	Check(context.Context, AuthenticationRequest) error
}

type AuthenticationRepository interface {
	GetProfile(context.Context, string) (IdentityProfile, error)
	SetAuthenticationState(context.Context, string, Status, AuthMethod) error
}

type AuthenticationCheckRequest struct {
	Alias     string
	Discovery Discovery
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
}

type DocumentedMetadata struct {
	LoginIdentity string
	Workspace     string
}

type DocumentedMetadataObserver interface {
	ObserveDocumentedMetadata(context.Context, AuthenticationRequest) (DocumentedMetadata, error)
}

type Repository interface {
	FindPendingProfile(context.Context, string) (PendingProfile, error)
	GetPendingProfile(context.Context, string) (PendingProfile, error)
	CreatePendingProfile(context.Context, PendingProfile) error
	SetManagedHome(context.Context, string, string, string) error
	SetReferencedHome(context.Context, string, string, string) error
	SaveDocumentedMetadata(context.Context, string, DocumentedMetadata) (bool, error)
	SaveSetupStage(context.Context, string, SetupStage) error
	ResetAuthentication(context.Context, string) error
	PromotePendingProfile(context.Context, string) (IdentityProfile, error)
	CompleteInitialSelection(context.Context, string) (IdentityProfile, error)
}

type SelectionRepository interface {
	ListEligibleProfiles(context.Context) ([]IdentityProfile, error)
	SelectProfile(context.Context, string) (SelectionResult, error)
}

type RegistryRepository interface {
	ListProfiles(context.Context) ([]IdentityProfile, error)
	EditProfile(context.Context, string, ProfileEdits) (IdentityProfile, error)
}

type Registry struct {
	repository RegistryRepository
}

func NewRegistry(repository RegistryRepository) (*Registry, error) {
	if repository == nil {
		return nil, apperrors.New(apperrors.ProfileSetupInvalid, ErrProfileStateInvalid)
	}
	return &Registry{repository: repository}, nil
}

func (registry *Registry) Inventory(ctx context.Context) (InventoryResult, error) {
	if registry == nil || registry.repository == nil {
		return InventoryResult{}, apperrors.New(apperrors.ProfileSetupInvalid, ErrProfileStateInvalid)
	}
	profiles, err := registry.repository.ListProfiles(contextOrBackground(ctx))
	return InventoryResult{Profiles: profiles}, err
}

func (registry *Registry) Edit(ctx context.Context, alias string, edits ProfileEdits) (IdentityProfile, error) {
	if registry == nil || registry.repository == nil {
		return IdentityProfile{}, apperrors.New(apperrors.ProfileSetupInvalid, ErrProfileStateInvalid)
	}
	if err := ValidateAlias(alias); err != nil {
		return IdentityProfile{}, err
	}
	if edits.Alias == nil && edits.DisplayName == nil && edits.Email == nil && edits.Workspace == nil {
		return IdentityProfile{}, apperrors.New(apperrors.ProfileSetupInvalid, ErrProfileStateInvalid)
	}
	if edits.Alias != nil {
		if err := ValidateAlias(*edits.Alias); err != nil {
			return IdentityProfile{}, err
		}
	}
	if edits.DisplayName != nil {
		value := strings.TrimSpace(*edits.DisplayName)
		if value == "" {
			return IdentityProfile{}, apperrors.New(apperrors.ProfileSetupInvalid, ErrProfileStateInvalid)
		}
		edits.DisplayName = &value
	}
	for _, field := range []*string{edits.Email, edits.Workspace} {
		if field != nil {
			*field = strings.TrimSpace(*field)
		}
	}
	return registry.repository.EditProfile(contextOrBackground(ctx), alias, edits)
}

type Selector struct {
	repository SelectionRepository
}

func NewSelector(repository SelectionRepository) (*Selector, error) {
	if repository == nil {
		return nil, apperrors.New(apperrors.ProfileNotSelectable, ErrNotSelectable)
	}
	return &Selector{repository: repository}, nil
}

type WorkflowOptions struct {
	Repository             Repository
	Discoverer             Discoverer
	HomeProvisioner        ManagedHomeProvisioner
	ReferencedHomeResolver ReferencedHomeResolver
	Authenticator          Authenticator
	IDGenerator            func() (string, error)
}

type Workflow struct {
	repository             Repository
	discoverer             Discoverer
	homeProvisioner        ManagedHomeProvisioner
	referencedHomeResolver ReferencedHomeResolver
	authenticator          Authenticator
	idGenerator            func() (string, error)
}

func NewWorkflow(options WorkflowOptions) (*Workflow, error) {
	if options.Repository == nil || options.Discoverer == nil || options.HomeProvisioner == nil || options.Authenticator == nil {
		return nil, apperrors.New(apperrors.ProfileSetupInvalid, errors.New("profile setup dependencies are incomplete"))
	}
	idGenerator := options.IDGenerator
	if idGenerator == nil {
		idGenerator = newProfileID
	}
	return &Workflow{
		repository:             options.Repository,
		discoverer:             options.Discoverer,
		homeProvisioner:        options.HomeProvisioner,
		referencedHomeResolver: options.ReferencedHomeResolver,
		authenticator:          options.Authenticator,
		idGenerator:            idGenerator,
	}, nil
}

func ValidateAlias(alias string) error {
	if alias == "" || alias != strings.TrimSpace(alias) || len(alias) > 64 {
		return apperrors.New(apperrors.ProfileAliasInvalid, ErrAliasInvalid)
	}
	for index, character := range []byte(alias) {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') {
			if index == 0 {
				continue
			}
			continue
		}
		if index == 0 || (character != '-' && character != '_' && character != '.') {
			return apperrors.New(apperrors.ProfileAliasInvalid, ErrAliasInvalid)
		}
	}
	return nil
}

func (workflow *Workflow) Add(ctx context.Context, request SetupRequest) (SetupResult, error) {
	if workflow == nil || workflow.repository == nil {
		return SetupResult{}, apperrors.New(apperrors.ProfileSetupInvalid, errors.New("profile setup workflow is unavailable"))
	}
	ctx = contextOrBackground(ctx)
	if err := ValidateAlias(request.Alias); err != nil {
		return SetupResult{}, err
	}
	if request.DisplayName != "" && len([]rune(request.DisplayName)) > 128 {
		return SetupResult{}, apperrors.New(apperrors.ProfileSetupInvalid, errors.New("profile display name is too long"))
	}
	method := normalizeAuthMethod(request.AuthMethod)
	if method != AuthMethodAutomatic && method != AuthMethodBrowser && method != AuthMethodDeviceCode {
		return SetupResult{}, apperrors.New(apperrors.ProfileSetupInvalid, errors.New("profile authentication method is invalid"))
	}

	pending, err := workflow.repository.FindPendingProfile(ctx, request.Alias)
	resumed := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return SetupResult{}, err
	}
	if !resumed {
		profileID, idErr := workflow.idGenerator()
		if idErr != nil || strings.TrimSpace(profileID) == "" {
			if idErr == nil {
				idErr = errors.New("profile identifier is empty")
			}
			return SetupResult{}, apperrors.New(apperrors.ProfileSetupInvalid, idErr)
		}
		displayName := request.DisplayName
		if displayName == "" {
			displayName = request.Alias
		}
		pending = PendingProfile{ID: profileID, Alias: request.Alias, DisplayName: displayName, Status: StatusPending}
		if err := workflow.repository.CreatePendingProfile(ctx, pending); err != nil {
			return SetupResult{}, err
		}
	} else if request.DisplayName != "" && request.DisplayName != pending.DisplayName {
		// Display-name editing is ticket #29; resumed setup keeps the durable
		// registration's original label.
		request.DisplayName = pending.DisplayName
	}

	discovery, err := workflow.discoverer.Discover(request.CodexOverride)
	if err != nil {
		return workflow.result(pending, discovery, resumed, ""), err
	}
	if !pending.Stages.Discovery {
		if err := workflow.repository.SaveSetupStage(ctx, pending.ID, StageDiscovery); err != nil {
			return workflow.result(pending, discovery, resumed, ""), err
		}
		pending.Stages.Discovery = true
	}

	homeOwnership := HomeOwnershipManaged
	var homePath string
	if pending.IdentityHomeOwnership == HomeOwnershipReferenced || strings.TrimSpace(request.ReferencedHomePath) != "" {
		homeOwnership = HomeOwnershipReferenced
		referencedPath := request.ReferencedHomePath
		if strings.TrimSpace(referencedPath) == "" {
			referencedPath = pending.IdentityHomePath
		}
		if workflow.referencedHomeResolver == nil {
			return workflow.result(pending, discovery, resumed, ""), apperrors.New(apperrors.ProfileHomeInvalid, ErrHomeInvalid)
		}
		homePath, err = workflow.referencedHomeResolver.Resolve(ctx, referencedPath)
	} else {
		homePath, err = workflow.homeProvisioner.Ensure(ctx, pending.ID)
	}
	if err != nil {
		return workflow.result(pending, discovery, resumed, ""), wrapHomeError(err)
	}
	if pending.IdentityHomeID == "" {
		var setHomeErr error
		if homeOwnership == HomeOwnershipReferenced {
			setHomeErr = workflow.repository.SetReferencedHome(ctx, pending.ID, pending.ID, homePath)
		} else {
			setHomeErr = workflow.repository.SetManagedHome(ctx, pending.ID, pending.ID, homePath)
		}
		if setHomeErr != nil {
			return workflow.result(pending, discovery, resumed, ""), setHomeErr
		}
		pending.IdentityHomeID = pending.ID
		pending.IdentityHomeOwnership = homeOwnership
		pending.IdentityHomePath = homePath
	} else if pending.IdentityHomeOwnership != homeOwnership || pending.IdentityHomePath == "" || filepath.Clean(pending.IdentityHomePath) != filepath.Clean(homePath) {
		return workflow.result(pending, discovery, resumed, ""), apperrors.New(apperrors.ProfileHomeInvalid, ErrHomeInvalid)
	}
	if !pending.Stages.Home {
		if err := workflow.repository.SaveSetupStage(ctx, pending.ID, StageHome); err != nil {
			return workflow.result(pending, discovery, resumed, ""), err
		}
		pending.Stages.Home = true
	}

	authenticationMethod, err := workflow.authenticateAndValidate(ctx, request, method, pending, discovery)
	if err != nil {
		return workflow.result(pending, discovery, resumed, authenticationMethod), err
	}
	pending.Stages.Authentication = true
	pending.Stages.Validation = true
	if authenticationRepository, ok := workflow.repository.(AuthenticationRepository); ok {
		persistedMethod := authenticationMethod
		if persistedMethod == AuthMethodReused {
			persistedMethod = ""
		}
		if err := authenticationRepository.SetAuthenticationState(ctx, pending.ID, StatusPending, persistedMethod); err != nil {
			return workflow.result(pending, discovery, resumed, authenticationMethod), err
		}
	}
	duplicate := false
	if observer, ok := workflow.authenticator.(DocumentedMetadataObserver); ok {
		metadata, metadataErr := observer.ObserveDocumentedMetadata(ctx, AuthenticationRequest{
			Discovery: discovery, IdentityHome: pending.IdentityHomePath, Method: authenticationMethod,
			Stdin: request.Stdin, Stdout: request.Stdout, Stderr: request.Stderr,
		})
		if metadataErr == nil && (strings.TrimSpace(metadata.LoginIdentity) != "" || strings.TrimSpace(metadata.Workspace) != "") {
			duplicate, err = workflow.repository.SaveDocumentedMetadata(ctx, pending.ID, metadata)
			if err != nil {
				return workflow.result(pending, discovery, resumed, authenticationMethod), err
			}
		}
	}

	ready, err := workflow.repository.PromotePendingProfile(ctx, pending.ID)
	if err != nil {
		return workflow.result(pending, discovery, resumed, authenticationMethod), err
	}
	selected, err := workflow.repository.CompleteInitialSelection(ctx, pending.ID)
	if err != nil {
		return workflow.result(pending, discovery, resumed, authenticationMethod), err
	}
	pending.Stages.Selection = true
	result := SetupResult{
		Profile:              selectedProfile(ready, selected),
		Discovery:            discovery,
		Stages:               pending.Stages,
		Resumed:              resumed,
		AuthenticationMethod: authenticationMethod,
	}
	if duplicate {
		result.Warnings = []string{ReferencedHomeDuplicateWarning}
	}
	return result, nil
}

func (selector *Selector) Eligible(ctx context.Context) ([]IdentityProfile, error) {
	if selector == nil || selector.repository == nil {
		return nil, apperrors.New(apperrors.ProfileNotSelectable, ErrNotSelectable)
	}
	return selector.repository.ListEligibleProfiles(contextOrBackground(ctx))
}

func (selector *Selector) Select(ctx context.Context, alias string) (SelectionResult, error) {
	if selector == nil || selector.repository == nil {
		return SelectionResult{}, apperrors.New(apperrors.ProfileNotSelectable, ErrNotSelectable)
	}
	if err := ValidateAlias(alias); err != nil {
		return SelectionResult{}, err
	}
	return selector.repository.SelectProfile(contextOrBackground(ctx), alias)
}

func Reauthenticate(ctx context.Context, repository AuthenticationRepository, discoverer Discoverer, authenticator Authenticator, request ReauthenticationRequest) (ReauthenticationResult, error) {
	if repository == nil || discoverer == nil || authenticator == nil {
		return ReauthenticationResult{}, apperrors.New(apperrors.ProfileSetupInvalid, errors.New("profile authentication dependencies are incomplete"))
	}
	ctx = contextOrBackground(ctx)
	if err := ValidateAlias(request.Alias); err != nil {
		return ReauthenticationResult{}, err
	}
	if !validAuthMethod(request.AuthMethod) {
		return ReauthenticationResult{}, apperrors.New(apperrors.ProfileSetupInvalid, errors.New("profile authentication method is invalid"))
	}

	item, err := repository.GetProfile(ctx, request.Alias)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ReauthenticationResult{}, apperrors.New(apperrors.ProfileNotSelectable, ErrNotSelectable)
		}
		return ReauthenticationResult{}, err
	}
	if item.Status == StatusPending || item.IdentityHomeID == "" || !filepath.IsAbs(item.IdentityHomePath) {
		return ReauthenticationResult{Profile: item}, apperrors.New(apperrors.ProfileNotSelectable, ErrNotSelectable)
	}
	discovery, err := discoverer.Discover(request.CodexOverride)
	if err != nil {
		return ReauthenticationResult{Profile: item}, err
	}
	authInput := request.Stdin
	if request.NonInteractive {
		authInput = nil
	}
	authRequest := AuthenticationRequest{
		Discovery:    discovery,
		IdentityHome: item.IdentityHomePath,
		Method:       AuthMethodReused,
		Stdin:        authInput,
		Stdout:       request.Stdout,
		Stderr:       request.Stderr,
	}
	if err := authenticator.Check(ctx, authRequest); err == nil {
		if item.Status != StatusReady {
			if err := repository.SetAuthenticationState(ctx, item.ID, StatusReady, ""); err != nil {
				return ReauthenticationResult{Profile: item, Discovery: discovery}, err
			}
			item.Status = StatusReady
		}
		return ReauthenticationResult{Profile: item, Discovery: discovery, AuthenticationMethod: AuthMethodReused}, nil
	} else if errors.Is(err, ErrAuthCancelled) || errors.Is(err, context.Canceled) {
		if stateErr := repository.SetAuthenticationState(ctx, item.ID, StatusNeedsReauthentication, ""); stateErr != nil {
			return ReauthenticationResult{Profile: item, Discovery: discovery}, stateErr
		}
		item.Status = StatusNeedsReauthentication
		return ReauthenticationResult{Profile: item, Discovery: discovery}, wrapAuthenticationError(err)
	} else if !errors.Is(err, ErrNotAuthenticated) {
		if stateErr := repository.SetAuthenticationState(ctx, item.ID, StatusUnavailable, ""); stateErr != nil {
			return ReauthenticationResult{Profile: item, Discovery: discovery}, stateErr
		}
		item.Status = StatusUnavailable
		return ReauthenticationResult{Profile: item, Discovery: discovery}, apperrors.New(apperrors.ProfileAuthenticationUnavailable, ErrAuthenticationUnavailable)
	}

	if err := repository.SetAuthenticationState(ctx, item.ID, StatusNeedsReauthentication, ""); err != nil {
		return ReauthenticationResult{Profile: item, Discovery: discovery}, err
	}
	item.Status = StatusNeedsReauthentication
	preferred := normalizeAuthMethod(request.AuthMethod)
	explicit := request.AuthMethod == AuthMethodBrowser || request.AuthMethod == AuthMethodDeviceCode
	if request.AuthMethod == "" {
		preferred = normalizeAuthMethod(item.AuthenticationMethod)
	}
	if request.NonInteractive && preferred == AuthMethodAutomatic {
		return ReauthenticationResult{Profile: item, Discovery: discovery}, apperrors.New(apperrors.ProfileSetupChoiceRequired, ErrSetupChoiceRequired)
	}
	method, err := authenticateUsingPreference(ctx, authenticator, authRequest, preferred, explicit)
	if err != nil {
		return ReauthenticationResult{Profile: item, Discovery: discovery, AuthenticationMethod: method}, err
	}
	authRequest.Method = method
	if err := authenticator.Check(ctx, authRequest); err != nil {
		if errors.Is(err, ErrAuthCancelled) {
			if stateErr := repository.SetAuthenticationState(ctx, item.ID, StatusNeedsReauthentication, ""); stateErr != nil {
				return ReauthenticationResult{Profile: item, Discovery: discovery, AuthenticationMethod: method}, stateErr
			}
			return ReauthenticationResult{Profile: item, Discovery: discovery, AuthenticationMethod: method}, wrapAuthenticationError(err)
		}
		if errors.Is(err, ErrNotAuthenticated) || errors.Is(err, context.Canceled) {
			if stateErr := repository.SetAuthenticationState(ctx, item.ID, StatusNeedsReauthentication, ""); stateErr != nil {
				return ReauthenticationResult{Profile: item, Discovery: discovery, AuthenticationMethod: method}, stateErr
			}
			return ReauthenticationResult{Profile: item, Discovery: discovery, AuthenticationMethod: method}, wrapValidationError(err)
		}
		if stateErr := repository.SetAuthenticationState(ctx, item.ID, StatusUnavailable, ""); stateErr != nil {
			return ReauthenticationResult{Profile: item, Discovery: discovery, AuthenticationMethod: method}, stateErr
		}
		return ReauthenticationResult{Profile: item, Discovery: discovery, AuthenticationMethod: method}, apperrors.New(apperrors.ProfileAuthenticationUnavailable, ErrAuthenticationUnavailable)
	}
	if err := repository.SetAuthenticationState(ctx, item.ID, StatusReady, method); err != nil {
		return ReauthenticationResult{Profile: item, Discovery: discovery, AuthenticationMethod: method}, err
	}
	item.Status = StatusReady
	item.AuthenticationMethod = method
	return ReauthenticationResult{Profile: item, Discovery: discovery, AuthenticationMethod: method, Reauthenticated: true}, nil
}

func VerifyAuthentication(ctx context.Context, repository AuthenticationRepository, authenticator Authenticator, request AuthenticationCheckRequest) (IdentityProfile, error) {
	if repository == nil || authenticator == nil {
		return IdentityProfile{}, apperrors.New(apperrors.ProfileAuthenticationUnavailable, ErrAuthenticationUnavailable)
	}
	ctx = contextOrBackground(ctx)
	item, err := repository.GetProfile(ctx, request.Alias)
	if err != nil {
		return IdentityProfile{}, err
	}
	if item.Status == StatusPending || item.IdentityHomeID == "" || !filepath.IsAbs(item.IdentityHomePath) {
		return item, apperrors.New(apperrors.LaunchProfileUnavailable, ErrNotSelectable)
	}
	err = authenticator.Check(ctx, AuthenticationRequest{
		Discovery:    request.Discovery,
		IdentityHome: item.IdentityHomePath,
		Method:       AuthMethodReused,
		Stdin:        request.Stdin,
		Stdout:       request.Stdout,
		Stderr:       request.Stderr,
	})
	if err == nil {
		if item.Status != StatusReady {
			if stateErr := repository.SetAuthenticationState(ctx, item.ID, StatusReady, ""); stateErr != nil {
				return item, stateErr
			}
			item.Status = StatusReady
		}
		return item, nil
	}
	if errors.Is(err, ErrNotAuthenticated) {
		if stateErr := repository.SetAuthenticationState(ctx, item.ID, StatusNeedsReauthentication, ""); stateErr != nil {
			return item, stateErr
		}
		item.Status = StatusNeedsReauthentication
		return item, apperrors.New(apperrors.ProfileReauthenticationRequired, ErrReauthenticationRequired)
	}
	if stateErr := repository.SetAuthenticationState(ctx, item.ID, StatusUnavailable, ""); stateErr != nil {
		return item, stateErr
	}
	item.Status = StatusUnavailable
	return item, apperrors.New(apperrors.ProfileAuthenticationUnavailable, ErrAuthenticationUnavailable)
}

func (workflow *Workflow) authenticateAndValidate(ctx context.Context, request SetupRequest, preferred AuthMethod, pending PendingProfile, discovery Discovery) (AuthMethod, error) {
	authRequest := AuthenticationRequest{
		Discovery:    discovery,
		IdentityHome: pending.IdentityHomePath,
		Stdin:        request.Stdin,
		Stdout:       request.Stdout,
		Stderr:       request.Stderr,
	}
	method := AuthMethodReused
	if pending.IdentityHomeOwnership == HomeOwnershipReferenced && !pending.Stages.Authentication && !pending.Stages.Validation && preferred == AuthMethodAutomatic {
		authRequest.Method = AuthMethodReused
		if err := workflow.authenticator.Check(ctx, authRequest); err == nil {
			if err := workflow.repository.SaveSetupStage(ctx, pending.ID, StageAuthentication); err != nil {
				return method, err
			}
			if err := workflow.repository.SaveSetupStage(ctx, pending.ID, StageValidation); err != nil {
				return method, err
			}
			return method, nil
		} else if !errors.Is(err, ErrNotAuthenticated) {
			return method, wrapValidationError(err)
		}
	}

	for attempt := 0; attempt < 2; attempt++ {
		if pending.Stages.Validation {
			authRequest.Method = AuthMethodReused
			if err := workflow.authenticator.Check(ctx, authRequest); err == nil {
				return method, nil
			} else if errors.Is(err, ErrNotAuthenticated) {
				if resetErr := workflow.repository.ResetAuthentication(ctx, pending.ID); resetErr != nil {
					return method, resetErr
				}
				pending.Stages.Authentication = false
				pending.Stages.Validation = false
			} else {
				return method, wrapValidationError(err)
			}
		}

		if !pending.Stages.Authentication {
			if request.NonInteractive && preferred == AuthMethodAutomatic {
				return method, apperrors.New(apperrors.ProfileSetupChoiceRequired, ErrSetupChoiceRequired)
			}
			selectedMethod, err := workflow.authenticate(ctx, authRequest, preferred)
			if err != nil {
				return selectedMethod, err
			}
			method = selectedMethod
			authRequest.Method = selectedMethod
			if err := workflow.repository.SaveSetupStage(ctx, pending.ID, StageAuthentication); err != nil {
				return method, err
			}
			pending.Stages.Authentication = true
		}

		if !pending.Stages.Validation {
			authRequest.Method = method
			if err := workflow.authenticator.Check(ctx, authRequest); err == nil {
				if err := workflow.repository.SaveSetupStage(ctx, pending.ID, StageValidation); err != nil {
					return method, err
				}
				return method, nil
			} else if errors.Is(err, ErrNotAuthenticated) {
				if resetErr := workflow.repository.ResetAuthentication(ctx, pending.ID); resetErr != nil {
					return method, resetErr
				}
				pending.Stages.Authentication = false
				pending.Stages.Validation = false
				continue
			} else {
				return method, wrapValidationError(err)
			}
		}
	}
	return method, wrapAuthenticationError(ErrAuthenticationFailed)
}

func (workflow *Workflow) authenticate(ctx context.Context, request AuthenticationRequest, preferred AuthMethod) (AuthMethod, error) {
	if preferred == AuthMethodBrowser || preferred == AuthMethodDeviceCode {
		request.Method = preferred
		if err := workflow.authenticator.Authenticate(ctx, request); err != nil {
			return preferred, wrapAuthenticationError(err)
		}
		return preferred, nil
	}

	request.Method = AuthMethodBrowser
	if err := workflow.authenticator.Authenticate(ctx, request); err == nil {
		return AuthMethodBrowser, nil
	} else if !errors.Is(err, ErrBrowserUnavailable) {
		return AuthMethodBrowser, wrapAuthenticationError(err)
	}

	request.Method = AuthMethodDeviceCode
	if err := workflow.authenticator.Authenticate(ctx, request); err != nil {
		return AuthMethodDeviceCode, wrapAuthenticationError(err)
	}
	return AuthMethodDeviceCode, nil
}

func authenticateUsingPreference(ctx context.Context, authenticator Authenticator, request AuthenticationRequest, preferred AuthMethod, explicit bool) (AuthMethod, error) {
	if preferred == AuthMethodDeviceCode {
		request.Method = AuthMethodDeviceCode
		if err := authenticator.Authenticate(ctx, request); err != nil {
			return preferred, wrapAuthenticationError(err)
		}
		return preferred, nil
	}
	if preferred != AuthMethodAutomatic && preferred != AuthMethodBrowser {
		return preferred, apperrors.New(apperrors.ProfileSetupInvalid, errors.New("profile authentication method is invalid"))
	}
	request.Method = AuthMethodBrowser
	if err := authenticator.Authenticate(ctx, request); err == nil {
		return AuthMethodBrowser, nil
	} else if !errors.Is(err, ErrBrowserUnavailable) || explicit {
		return AuthMethodBrowser, wrapAuthenticationError(err)
	}
	request.Method = AuthMethodDeviceCode
	if err := authenticator.Authenticate(ctx, request); err != nil {
		return AuthMethodDeviceCode, wrapAuthenticationError(err)
	}
	return AuthMethodDeviceCode, nil
}

func (workflow *Workflow) result(pending PendingProfile, discovery Discovery, resumed bool, method AuthMethod) SetupResult {
	return SetupResult{
		Profile: IdentityProfile{
			ID:                    pending.ID,
			Alias:                 pending.Alias,
			DisplayName:           pending.DisplayName,
			Status:                pending.Status,
			IdentityHomeID:        pending.IdentityHomeID,
			IdentityHomeOwnership: pending.IdentityHomeOwnership,
		},
		Discovery:            discovery,
		Stages:               pending.Stages,
		Resumed:              resumed,
		AuthenticationMethod: method,
	}
}

func selectedProfile(ready, selected IdentityProfile) IdentityProfile {
	if selected.ID == "" {
		return ready
	}
	return selected
}

func normalizeAuthMethod(method AuthMethod) AuthMethod {
	if method == "" {
		return AuthMethodAutomatic
	}
	return method
}

func validAuthMethod(method AuthMethod) bool {
	return method == "" || method == AuthMethodAutomatic || method == AuthMethodBrowser || method == AuthMethodDeviceCode
}

func equalAlias(left, right string) bool {
	return strings.EqualFold(left, right)
}

func wrapHomeError(err error) error {
	if apperrors.Code(err) != "" {
		return err
	}
	return apperrors.New(apperrors.ProfileHomeInvalid, errors.Join(ErrHomeInvalid, err))
}

func wrapAuthenticationError(err error) error {
	if errors.Is(err, ErrAuthCancelled) || errors.Is(err, context.Canceled) {
		return apperrors.New(apperrors.ProfileAuthenticationCancelled, errors.Join(ErrAuthCancelled, err))
	}
	if apperrors.Code(err) != "" {
		return err
	}
	return apperrors.New(apperrors.ProfileAuthenticationFailed, errors.Join(ErrAuthenticationFailed, err))
}

func wrapValidationError(err error) error {
	if errors.Is(err, context.Canceled) {
		return apperrors.New(apperrors.ProfileAuthenticationCancelled, errors.Join(ErrAuthCancelled, err))
	}
	if apperrors.Code(err) != "" {
		return err
	}
	return apperrors.New(apperrors.ProfileValidationFailed, errors.Join(ErrValidationFailed, err))
}

func newProfileID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	random[6] = (random[6] & 0x0f) | 0x40
	random[8] = (random[8] & 0x3f) | 0x80
	return hex.EncodeToString(random[0:4]) + "-" +
		hex.EncodeToString(random[4:6]) + "-" +
		hex.EncodeToString(random[6:8]) + "-" +
		hex.EncodeToString(random[8:10]) + "-" +
		hex.EncodeToString(random[10:16]), nil
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
