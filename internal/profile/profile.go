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

const HomeOwnershipManaged HomeOwnership = "managed"

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
	Status                Status        `json:"status"`
	IdentityHomeID        string        `json:"identity_home_id"`
	IdentityHomeOwnership HomeOwnership `json:"identity_home_ownership"`
	Selected              bool          `json:"selected"`
	CreatedAt             time.Time     `json:"created_at"`
	UpdatedAt             time.Time     `json:"updated_at"`
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
	Alias          string
	DisplayName    string
	CodexOverride  string
	AuthMethod     AuthMethod
	NonInteractive bool
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
}

type SetupResult struct {
	Profile              IdentityProfile `json:"profile"`
	Discovery            Discovery       `json:"discovery"`
	Stages               SetupStages     `json:"stages"`
	Resumed              bool            `json:"resumed"`
	AuthenticationMethod AuthMethod      `json:"authentication_method,omitempty"`
}

var (
	ErrNotFound             = errors.New("profile was not found")
	ErrAliasInvalid         = errors.New("profile alias is invalid")
	ErrAliasTaken           = errors.New("profile alias is already in use")
	ErrSetupChoiceRequired  = errors.New("profile setup requires an explicit choice")
	ErrAuthCancelled        = errors.New("Codex authentication was cancelled")
	ErrAuthenticationFailed = errors.New("Codex authentication failed")
	ErrBrowserUnavailable   = errors.New("Codex browser authentication is unavailable")
	ErrNotAuthenticated     = errors.New("Codex authentication is not usable")
	ErrHomeInvalid          = errors.New("managed Identity Home is invalid")
	ErrValidationFailed     = errors.New("Identity Home validation failed")
)

type Discoverer interface {
	Discover(override string) (Discovery, error)
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

type Repository interface {
	FindPendingProfile(context.Context, string) (PendingProfile, error)
	GetPendingProfile(context.Context, string) (PendingProfile, error)
	CreatePendingProfile(context.Context, PendingProfile) error
	SetManagedHome(context.Context, string, string, string) error
	SaveSetupStage(context.Context, string, SetupStage) error
	ResetAuthentication(context.Context, string) error
	PromotePendingProfile(context.Context, string) (IdentityProfile, error)
	CompleteInitialSelection(context.Context, string) (IdentityProfile, error)
}

type WorkflowOptions struct {
	Repository      Repository
	Discoverer      Discoverer
	HomeProvisioner ManagedHomeProvisioner
	Authenticator   Authenticator
	IDGenerator     func() (string, error)
}

type Workflow struct {
	repository      Repository
	discoverer      Discoverer
	homeProvisioner ManagedHomeProvisioner
	authenticator   Authenticator
	idGenerator     func() (string, error)
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
		repository:      options.Repository,
		discoverer:      options.Discoverer,
		homeProvisioner: options.HomeProvisioner,
		authenticator:   options.Authenticator,
		idGenerator:     idGenerator,
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

	homePath, err := workflow.homeProvisioner.Ensure(ctx, pending.ID)
	if err != nil {
		return workflow.result(pending, discovery, resumed, ""), wrapHomeError(err)
	}
	if pending.IdentityHomeID == "" {
		if err := workflow.repository.SetManagedHome(ctx, pending.ID, pending.ID, homePath); err != nil {
			return workflow.result(pending, discovery, resumed, ""), err
		}
		pending.IdentityHomeID = pending.ID
		pending.IdentityHomeOwnership = HomeOwnershipManaged
		pending.IdentityHomePath = homePath
	} else if pending.IdentityHomePath == "" || filepath.Clean(pending.IdentityHomePath) != filepath.Clean(homePath) {
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

	ready, err := workflow.repository.PromotePendingProfile(ctx, pending.ID)
	if err != nil {
		return workflow.result(pending, discovery, resumed, authenticationMethod), err
	}
	selected, err := workflow.repository.CompleteInitialSelection(ctx, pending.ID)
	if err != nil {
		return workflow.result(pending, discovery, resumed, authenticationMethod), err
	}
	pending.Stages.Selection = true
	return SetupResult{
		Profile:              selectedProfile(ready, selected),
		Discovery:            discovery,
		Stages:               pending.Stages,
		Resumed:              resumed,
		AuthenticationMethod: authenticationMethod,
	}, nil
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
