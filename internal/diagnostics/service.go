package diagnostics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

const (
	DefaultRetentionDays  = 14
	MinimumRetentionDays  = 1
	MaximumRetentionDays  = 30
	MaxBundleEncodedBytes = 50 * 1024 * 1024
	BundleSchemaVersion   = 1
)

// Level is the minimum diagnostic severity retained by a configured sink.
type Level string

const (
	LevelInfo    Level = "info"
	LevelWarning Level = "warning"
	LevelError   Level = "error"
)

// Settings are independent local diagnostic controls. Zero-value Settings
// are not defaults: use DefaultSettings when constructing an update.
type Settings struct {
	Enabled       bool  `json:"enabled"`
	MinimumLevel  Level `json:"minimum_level"`
	RetentionDays int   `json:"retention_days"`
}

func DefaultSettings() Settings {
	return Settings{Enabled: true, MinimumLevel: LevelInfo, RetentionDays: DefaultRetentionDays}
}

func (settings Settings) Validate() error {
	if !validLevel(settings.MinimumLevel) || settings.RetentionDays < MinimumRetentionDays || settings.RetentionDays > MaximumRetentionDays {
		return configurationError("diagnostic settings are outside the supported bounds")
	}
	return nil
}

func validLevel(level Level) bool {
	return level == LevelInfo || level == LevelWarning || level == LevelError
}

// Repository is the complete persistence boundary needed by Service.
type Repository interface {
	DiagnosticSettings(context.Context) (Settings, error)
	SetDiagnosticSettings(context.Context, Settings) (Settings, error)
	ListDiagnosticAggregates(context.Context) ([]Aggregate, error)
}

type OSFamily string

const (
	OSLinux   OSFamily = "linux"
	OSWindows OSFamily = "windows"
	OSDarwin  OSFamily = "darwin"
)

type Architecture string

const (
	ArchitectureAMD64 Architecture = "amd64"
	ArchitectureARM64 Architecture = "arm64"
)

type HealthState string

const (
	HealthHealthy     HealthState = "healthy"
	HealthDegraded    HealthState = "degraded"
	HealthUnavailable HealthState = "unavailable"
	HealthLocked      HealthState = "locked"
	HealthDisabled    HealthState = "disabled"
)

// FeatureStates contains only coarse local enablement state.
type FeatureStates struct {
	Service          bool `json:"service"`
	DetailedAlerts   bool `json:"detailed_alerts"`
	AutomaticUpdates bool `json:"automatic_updates"`
	Telemetry        bool `json:"telemetry"`
}

// Health contains only allowlisted operational states, never causes or
// free-form messages.
type Health struct {
	Service   HealthState `json:"service"`
	Database  HealthState `json:"database"`
	Vault     HealthState `json:"vault"`
	ErrorCode string      `json:"error_code,omitempty"`
}

// BundleEnvironment is supplied by the composition root from trusted local
// runtime state and is validated before a preview can be created.
type BundleEnvironment struct {
	ApplicationVersion    string        `json:"application_version"`
	DatabaseSchemaVersion int           `json:"database_schema_version"`
	OSFamily              OSFamily      `json:"os_family"`
	Architecture          Architecture  `json:"architecture"`
	Features              FeatureStates `json:"features"`
	Health                Health        `json:"health"`
}

// Bundle is the fixed, versioned support evidence document.
type Bundle struct {
	SchemaVersion int               `json:"schema_version"`
	GeneratedAt   time.Time         `json:"generated_at"`
	Settings      Settings          `json:"settings"`
	Environment   BundleEnvironment `json:"environment"`
	Diagnostics   []Aggregate       `json:"diagnostics"`
}

// Preview describes and retains the exact document eligible for confirmed
// export. Fields is a fixed schema inventory rather than data-derived keys.
type Preview struct {
	Fields             []string `json:"fields"`
	DiagnosticCount    int      `json:"diagnostic_count"`
	EncodedBytes       int      `json:"encoded_bytes"`
	ConfirmationDigest string   `json:"confirmation_digest"`
	Bundle             Bundle   `json:"bundle"`
}

type ServiceOptions struct {
	Repository  Repository
	Clock       Clock
	Environment func(context.Context) (BundleEnvironment, error)
}

type Service struct {
	repository  Repository
	clock       Clock
	environment func(context.Context) (BundleEnvironment, error)
	mu          sync.Mutex
	preview     *Preview
}

func NewService(options ServiceOptions) (*Service, error) {
	if options.Repository == nil {
		return nil, configurationError("diagnostic repository is required")
	}
	if options.Environment == nil {
		return nil, configurationError("diagnostic environment provider is required")
	}
	clock := options.Clock
	if clock == nil {
		clock = systemClock{}
	}
	return &Service{repository: options.Repository, clock: clock, environment: options.Environment}, nil
}

func (environment BundleEnvironment) Validate() error {
	if !safeVersion(environment.ApplicationVersion) || environment.DatabaseSchemaVersion < 1 || environment.DatabaseSchemaVersion > 1_000_000 {
		return configurationError("diagnostic bundle version metadata is invalid")
	}
	if environment.OSFamily != OSLinux && environment.OSFamily != OSWindows && environment.OSFamily != OSDarwin {
		return configurationError("diagnostic bundle OS family is invalid")
	}
	if environment.Architecture != ArchitectureAMD64 && environment.Architecture != ArchitectureARM64 {
		return configurationError("diagnostic bundle architecture is invalid")
	}
	if !validHealth(environment.Health.Service, true) || !validHealth(environment.Health.Database, false) || !validHealth(environment.Health.Vault, true) {
		return configurationError("diagnostic bundle health is invalid")
	}
	if environment.Health.ErrorCode != "" && !apperrors.IsRegistered(environment.Health.ErrorCode) {
		return configurationError("diagnostic bundle error code is invalid")
	}
	return nil
}

func safeVersion(value string) bool {
	if value == "" || len(value) > 64 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune(".-+_", character) {
			continue
		}
		return false
	}
	return true
}

func validHealth(state HealthState, allowLockedDisabled bool) bool {
	if state == HealthHealthy || state == HealthDegraded || state == HealthUnavailable {
		return true
	}
	return allowLockedDisabled && (state == HealthLocked || state == HealthDisabled)
}

func (service *Service) Settings(ctx context.Context) (Settings, error) {
	if service == nil || service.repository == nil {
		return Settings{}, configurationError("diagnostic service is unavailable")
	}
	return service.repository.DiagnosticSettings(ctx)
}

func (service *Service) UpdateSettings(ctx context.Context, settings Settings) (Settings, error) {
	if service == nil || service.repository == nil {
		return Settings{}, configurationError("diagnostic service is unavailable")
	}
	if err := settings.Validate(); err != nil {
		return Settings{}, err
	}
	updated, err := service.repository.SetDiagnosticSettings(ctx, settings)
	if err != nil {
		return Settings{}, err
	}
	service.mu.Lock()
	service.preview = nil
	service.mu.Unlock()
	return updated, nil
}

func (service *Service) Preview(ctx context.Context) (Preview, error) {
	if service == nil || service.repository == nil {
		return Preview{}, configurationError("diagnostic service is unavailable")
	}
	settings, err := service.repository.DiagnosticSettings(ctx)
	if err != nil {
		return Preview{}, err
	}
	aggregates, err := service.repository.ListDiagnosticAggregates(ctx)
	if err != nil {
		return Preview{}, err
	}
	for _, aggregate := range aggregates {
		if err := aggregate.Validate(); err != nil {
			return Preview{}, err
		}
	}
	confirmedSettings, err := service.repository.DiagnosticSettings(ctx)
	if err != nil {
		return Preview{}, err
	}
	if settings != confirmedSettings {
		return Preview{}, configurationError("diagnostic settings changed while previewing")
	}
	environment, err := service.environment(ctx)
	if err != nil {
		return Preview{}, apperrors.New(apperrors.DiagnosticsConfigurationInvalid, errors.Join(errors.New("diagnostic environment is unavailable"), err))
	}
	if err := environment.Validate(); err != nil {
		return Preview{}, err
	}
	bundle := Bundle{
		SchemaVersion: BundleSchemaVersion,
		GeneratedAt:   service.clock.Now().UTC(),
		Settings:      settings,
		Environment:   environment,
		Diagnostics:   cloneAggregates(aggregates),
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		return Preview{}, configurationError("diagnostic bundle could not be encoded")
	}
	if len(encoded) > MaxBundleEncodedBytes {
		return Preview{}, configurationError("diagnostic bundle exceeds the encoded size limit")
	}
	digest := sha256.Sum256(encoded)
	preview := Preview{
		Fields: []string{
			"schema_version", "generated_at", "settings", "environment.application_version", "environment.database_schema_version",
			"environment.os_family", "environment.architecture", "environment.features", "environment.health", "diagnostics",
		},
		DiagnosticCount:    len(bundle.Diagnostics),
		EncodedBytes:       len(encoded),
		ConfirmationDigest: hex.EncodeToString(digest[:]),
		Bundle:             bundle,
	}
	service.mu.Lock()
	copy := clonePreview(preview)
	service.preview = &copy
	service.mu.Unlock()
	return clonePreview(preview), nil
}

// Export returns only the exact cached bundle represented by the most recent
// preview. Callers own any explicit local write or browser download.
func (service *Service) Export(ctx context.Context, confirmationDigest string) (Bundle, error) {
	if service == nil {
		return Bundle{}, configurationError("diagnostic service is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return Bundle{}, apperrors.New(apperrors.DiagnosticsExportFailed, err)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.preview == nil || confirmationDigest == "" || confirmationDigest != service.preview.ConfirmationDigest {
		return Bundle{}, apperrors.New(apperrors.DiagnosticsConfirmationInvalid, errors.New("diagnostic export confirmation is missing or stale"))
	}
	return cloneBundle(service.preview.Bundle), nil
}

func configurationError(reason string) error {
	return apperrors.New(apperrors.DiagnosticsConfigurationInvalid, errors.New(reason))
}

func clonePreview(preview Preview) Preview {
	preview.Fields = append([]string(nil), preview.Fields...)
	preview.Bundle = cloneBundle(preview.Bundle)
	return preview
}

func cloneBundle(bundle Bundle) Bundle {
	bundle.Diagnostics = cloneAggregates(bundle.Diagnostics)
	return bundle
}

func cloneAggregates(aggregates []Aggregate) []Aggregate {
	return append([]Aggregate(nil), aggregates...)
}
