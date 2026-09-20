// Package telemetry owns the privacy boundary for optional product-health
// telemetry. Events are closed structs: callers cannot attach arbitrary
// identity, content, path, command, or usage fields.
package telemetry

import (
	"errors"
	"regexp"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

const (
	SchemaVersion            = 1
	ConsentSchemaVersion     = 1
	EventRetentionDays       = 30
	AggregateRetentionMonths = 13

	MaxAppVersionLength = 64
	MaxErrorCodeLength  = 64
	InstallationIDBytes = 16
)

var (
	ErrInvalid              = errors.New("telemetry value is invalid")
	ErrPrerequisitesMissing = errors.New("telemetry prerequisites are unavailable")
	ErrConsentRequired      = errors.New("explicit telemetry consent is required")
	ErrClosed               = errors.New("telemetry service is closed")
	ErrTransportDisabled    = errors.New("telemetry transport is disabled")

	appVersionPattern    = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]*$`)
	errorCodePattern     = regexp.MustCompile(`^CF_[A-Z0-9_]+$`)
	installationIDRegexp = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

type OSFamily string

const (
	OSLinux   OSFamily = "linux"
	OSMacOS   OSFamily = "macos"
	OSWindows OSFamily = "windows"
)

type Architecture string

const (
	ArchitectureAMD64 Architecture = "amd64"
	ArchitectureARM64 Architecture = "arm64"
)

// Feature is intentionally coarse. It describes a shipped capability, never
// a workspace, project, command, session, or item within that capability.
type Feature string

const (
	FeatureDashboard          Feature = "dashboard"
	FeatureActivity           Feature = "activity"
	FeatureProfiles           Feature = "profiles"
	FeatureConfigurationPacks Feature = "configuration_packs"
	FeatureDiagnostics        Feature = "diagnostics"
	FeatureUpdates            Feature = "updates"
)

type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
	OutcomeCancelled Outcome = "cancelled"
)

type DurationBucket string

const (
	DurationUnder100Milliseconds DurationBucket = "under_100_ms"
	DurationUnderOneSecond       DurationBucket = "100_ms_to_1_s"
	DurationUnderTenSeconds      DurationBucket = "1_s_to_10_s"
	DurationTenSecondsOrMore     DurationBucket = "10_s_or_more"
)

// EventV1 is the complete public wire schema. Do not add a metadata map or raw
// payload: its absence is what makes excluded data unrepresentable.
type EventV1 struct {
	SchemaVersion  int            `json:"schema_version"`
	AppVersion     string         `json:"app_version"`
	OSFamily       OSFamily       `json:"os_family"`
	Architecture   Architecture   `json:"architecture"`
	Feature        Feature        `json:"feature"`
	Outcome        Outcome        `json:"outcome"`
	ErrorCode      string         `json:"error_code,omitempty"`
	DurationBucket DurationBucket `json:"duration_bucket"`
	InstallationID string         `json:"installation_id"`
}

func (event EventV1) Validate() error {
	if event.SchemaVersion != SchemaVersion || !validAppVersion(event.AppVersion) ||
		!validOS(event.OSFamily) || !validArchitecture(event.Architecture) ||
		!validFeature(event.Feature) || !validOutcome(event.Outcome) ||
		!validDuration(event.DurationBucket) || !installationIDRegexp.MatchString(event.InstallationID) {
		return ErrInvalid
	}
	if event.ErrorCode != "" && (len(event.ErrorCode) > MaxErrorCodeLength || !errorCodePattern.MatchString(event.ErrorCode) || !apperrors.IsRegistered(event.ErrorCode)) {
		return ErrInvalid
	}
	if event.Outcome != OutcomeFailed && event.ErrorCode != "" {
		return ErrInvalid
	}
	return nil
}

func validAppVersion(value string) bool {
	return value != "" && len(value) <= MaxAppVersionLength && strings.TrimSpace(value) == value && appVersionPattern.MatchString(value)
}

func validOS(value OSFamily) bool { return value == OSLinux || value == OSMacOS || value == OSWindows }

func validArchitecture(value Architecture) bool {
	return value == ArchitectureAMD64 || value == ArchitectureARM64
}

func validFeature(value Feature) bool {
	switch value {
	case FeatureDashboard, FeatureActivity, FeatureProfiles, FeatureConfigurationPacks, FeatureDiagnostics, FeatureUpdates:
		return true
	default:
		return false
	}
}

func validOutcome(value Outcome) bool {
	return value == OutcomeSucceeded || value == OutcomeFailed || value == OutcomeCancelled
}

func validDuration(value DurationBucket) bool {
	switch value {
	case DurationUnder100Milliseconds, DurationUnderOneSecond, DurationUnderTenSeconds, DurationTenSecondsOrMore:
		return true
	default:
		return false
	}
}

type PublicSchema struct {
	Version                  int              `json:"version"`
	ConsentVersion           int              `json:"consent_version"`
	EventRetentionDays       int              `json:"event_retention_days"`
	AggregateRetentionMonths int              `json:"aggregate_retention_months"`
	Fields                   []string         `json:"fields"`
	OSFamilies               []OSFamily       `json:"os_families"`
	Architectures            []Architecture   `json:"architectures"`
	Features                 []Feature        `json:"features"`
	Outcomes                 []Outcome        `json:"outcomes"`
	DurationBuckets          []DurationBucket `json:"duration_buckets"`
}

func Schema() PublicSchema {
	return PublicSchema{
		Version:                  SchemaVersion,
		ConsentVersion:           ConsentSchemaVersion,
		EventRetentionDays:       EventRetentionDays,
		AggregateRetentionMonths: AggregateRetentionMonths,
		Fields:                   []string{"schema_version", "app_version", "os_family", "architecture", "feature", "outcome", "error_code", "duration_bucket", "installation_id"},
		OSFamilies:               []OSFamily{OSLinux, OSMacOS, OSWindows},
		Architectures:            []Architecture{ArchitectureAMD64, ArchitectureARM64},
		Features:                 []Feature{FeatureDashboard, FeatureActivity, FeatureProfiles, FeatureConfigurationPacks, FeatureDiagnostics, FeatureUpdates},
		Outcomes:                 []Outcome{OutcomeSucceeded, OutcomeFailed, OutcomeCancelled},
		DurationBuckets:          []DurationBucket{DurationUnder100Milliseconds, DurationUnderOneSecond, DurationUnderTenSeconds, DurationTenSecondsOrMore},
	}
}
