// Package apperrors owns stable machine-readable application error identifiers.
package apperrors

import "errors"

const (
	CLIUsage                                 = "CF_CLI_USAGE"
	CLIInternal                              = "CF_CLI_INTERNAL"
	CLIShellIntegrationInvalid               = "CF_CLI_SHELL_INTEGRATION_INVALID"
	CLIShellIntegrationFailed                = "CF_CLI_SHELL_INTEGRATION_FAILED"
	LaunchCodexNotFound                      = "CF_LAUNCH_CODEX_NOT_FOUND"
	LaunchCodexAmbiguous                     = "CF_LAUNCH_CODEX_AMBIGUOUS"
	LaunchCodexPathInvalid                   = "CF_LAUNCH_CODEX_PATH_INVALID"
	LaunchCodexVersionInvalid                = "CF_LAUNCH_CODEX_VERSION_INVALID"
	LaunchPlanInvalid                        = "CF_LAUNCH_PLAN_INVALID"
	LaunchLeaseInvalid                       = "CF_LAUNCH_LEASE_INVALID"
	LaunchProfileNotFound                    = "CF_LAUNCH_PROFILE_NOT_FOUND"
	LaunchProfileUnavailable                 = "CF_LAUNCH_PROFILE_UNAVAILABLE"
	LaunchProcessStartFailed                 = "CF_LAUNCH_PROCESS_START_FAILED"
	LaunchProcessStatusInvalid               = "CF_LAUNCH_PROCESS_STATUS_INVALID"
	ProfileSetupInvalid                      = "CF_PROFILE_SETUP_INVALID"
	ProfileAliasInvalid                      = "CF_PROFILE_ALIAS_INVALID"
	ProfileAliasTaken                        = "CF_PROFILE_ALIAS_TAKEN"
	ProfileNotSelectable                     = "CF_PROFILE_NOT_SELECTABLE"
	ProfileSetupChoiceRequired               = "CF_PROFILE_SETUP_CHOICE_REQUIRED"
	ProfileAuthenticationFailed              = "CF_PROFILE_AUTHENTICATION_FAILED"
	ProfileAuthenticationCancelled           = "CF_PROFILE_AUTHENTICATION_CANCELLED"
	ProfileReauthenticationRequired          = "CF_PROFILE_REAUTHENTICATION_REQUIRED"
	ProfileAuthenticationUnavailable         = "CF_PROFILE_AUTHENTICATION_UNAVAILABLE"
	ProfileHomeInvalid                       = "CF_PROFILE_HOME_INVALID"
	ProfileValidationFailed                  = "CF_PROFILE_VALIDATION_FAILED"
	ProfileRemovalBlocked                    = "CF_PROFILE_REMOVAL_BLOCKED"
	ProfileReplacementRequired               = "CF_PROFILE_REPLACEMENT_REQUIRED"
	ProfileConfirmationInvalid               = "CF_PROFILE_CONFIRMATION_INVALID"
	ProfileQuarantineInvalid                 = "CF_PROFILE_QUARANTINE_INVALID"
	ProfileQuarantineExpired                 = "CF_PROFILE_QUARANTINE_EXPIRED"
	ConfigurationPackInvalid                 = "CF_CONFIGPACK_INVALID"
	ConfigurationPackNotFound                = "CF_CONFIGPACK_NOT_FOUND"
	ConfigurationPackNotApproved             = "CF_CONFIGPACK_NOT_APPROVED"
	ConfigurationPackAssignmentInvalid       = "CF_CONFIGPACK_ASSIGNMENT_INVALID"
	ConfigurationPackProjectionFailed        = "CF_CONFIGPACK_PROJECTION_FAILED"
	ConfigurationPackPromotionReviewRequired = "CF_CONFIGPACK_PROMOTION_REVIEW_REQUIRED"
	DiagnosticsConfigurationInvalid          = "CF_DIAGNOSTICS_CONFIGURATION_INVALID"
	DiagnosticsEventInvalid                  = "CF_DIAGNOSTICS_EVENT_INVALID"
	PlatformStatePathInvalid                 = "CF_PLATFORM_STATE_PATH_INVALID"
	PlatformStatePathUnsafe                  = "CF_PLATFORM_STATE_PATH_UNSAFE"
	PlatformPermissionDenied                 = "CF_PLATFORM_PERMISSION_DENIED"
	PlatformServiceAlreadyRunning            = "CF_PLATFORM_SERVICE_ALREADY_RUNNING"
	PlatformServiceMetadataInvalid           = "CF_PLATFORM_SERVICE_METADATA_INVALID"
	PlatformServiceUnavailable               = "CF_PLATFORM_SERVICE_UNAVAILABLE"
	HTTPAPIHostInvalid                       = "CF_HTTPAPI_HOST_INVALID"
	HTTPAPIOriginInvalid                     = "CF_HTTPAPI_ORIGIN_INVALID"
	HTTPAPIBootstrapInvalid                  = "CF_HTTPAPI_BOOTSTRAP_INVALID"
	HTTPAPISessionInvalid                    = "CF_HTTPAPI_SESSION_INVALID"
	HTTPAPISessionExpired                    = "CF_HTTPAPI_SESSION_EXPIRED"
	HTTPAPICSRFInvalid                       = "CF_HTTPAPI_CSRF_INVALID"
	HTTPAPIMethodNotAllowed                  = "CF_HTTPAPI_METHOD_NOT_ALLOWED"
	HTTPAPIRouteNotFound                     = "CF_HTTPAPI_ROUTE_NOT_FOUND"
	HTTPAPIServiceUnavailable                = "CF_HTTPAPI_SERVICE_UNAVAILABLE"
	StoreOpenFailed                          = "CF_STORE_OPEN_FAILED"
	StoreIntegrityFailed                     = "CF_STORE_INTEGRITY_FAILED"
	StoreSchemaIncompatible                  = "CF_STORE_SCHEMA_INCOMPATIBLE"
	StoreMigrationFailed                     = "CF_STORE_MIGRATION_FAILED"
	StoreMigrationPartial                    = "CF_STORE_MIGRATION_PARTIAL"
	StoreReadFailed                          = "CF_STORE_READ_FAILED"
	StoreWriteFailed                         = "CF_STORE_WRITE_FAILED"
	StoreDiagnosticWriteFailed               = "CF_STORE_DIAGNOSTIC_WRITE_FAILED"
	StoreBackupFailed                        = "CF_STORE_BACKUP_FAILED"
	StoreRecoveryCandidateInvalid            = "CF_STORE_RECOVERY_CANDIDATE_INVALID"
	StoreRecoveryCandidateNotFound           = "CF_STORE_RECOVERY_CANDIDATE_NOT_FOUND"
	StoreRecoveryRestoreFailed               = "CF_STORE_RECOVERY_RESTORE_FAILED"
	VaultUnavailable                         = "CF_VAULT_UNAVAILABLE"
	VaultLocked                              = "CF_VAULT_LOCKED"
	VaultKeyInvalid                          = "CF_VAULT_KEY_INVALID"
	VaultEnvelopeInvalid                     = "CF_VAULT_ENVELOPE_INVALID"
	VaultEnvelopeUnsupported                 = "CF_VAULT_ENVELOPE_UNSUPPORTED"
	VaultKeyGenerationMismatch               = "CF_VAULT_KEY_GENERATION_MISMATCH"
	VaultEncryptionFailed                    = "CF_VAULT_ENCRYPTION_FAILED"
)

// CodedError carries a stable identifier while keeping implementation details
// out of user-facing diagnostics. Callers can still inspect the underlying
// cause with errors.Is or errors.As when they need to make a local decision.
type CodedError struct {
	code  string
	cause error
}

func New(code string, cause error) error {
	return &CodedError{code: code, cause: cause}
}

func (err *CodedError) Error() string {
	return err.code
}

func (err *CodedError) Unwrap() error {
	return err.cause
}

// Code returns the stable identifier carried by err, or an empty string when
// err is not a coded application error.
func Code(err error) string {
	var coded *CodedError
	if errors.As(err, &coded) {
		return coded.code
	}
	return ""
}

// IsRegistered reports whether code is one of the stable identifiers owned by
// the current build. It keeps diagnostic producers from emitting arbitrary
// strings as if they were part of the compatibility contract.
func IsRegistered(code string) bool {
	switch code {
	case CLIUsage,
		CLIInternal,
		CLIShellIntegrationInvalid,
		CLIShellIntegrationFailed,
		LaunchCodexNotFound,
		LaunchCodexAmbiguous,
		LaunchCodexPathInvalid,
		LaunchCodexVersionInvalid,
		LaunchPlanInvalid,
		LaunchLeaseInvalid,
		LaunchProfileNotFound,
		LaunchProfileUnavailable,
		LaunchProcessStartFailed,
		LaunchProcessStatusInvalid,
		ProfileSetupInvalid,
		ProfileAliasInvalid,
		ProfileAliasTaken,
		ProfileNotSelectable,
		ProfileSetupChoiceRequired,
		ProfileAuthenticationFailed,
		ProfileAuthenticationCancelled,
		ProfileReauthenticationRequired,
		ProfileAuthenticationUnavailable,
		ProfileHomeInvalid,
		ProfileValidationFailed,
		ProfileRemovalBlocked,
		ProfileReplacementRequired,
		ProfileConfirmationInvalid,
		ProfileQuarantineInvalid,
		ProfileQuarantineExpired,
		ConfigurationPackInvalid,
		ConfigurationPackNotFound,
		ConfigurationPackNotApproved,
		ConfigurationPackAssignmentInvalid,
		ConfigurationPackProjectionFailed,
		ConfigurationPackPromotionReviewRequired,
		DiagnosticsConfigurationInvalid,
		DiagnosticsEventInvalid,
		PlatformStatePathInvalid,
		PlatformStatePathUnsafe,
		PlatformPermissionDenied,
		PlatformServiceAlreadyRunning,
		PlatformServiceMetadataInvalid,
		PlatformServiceUnavailable,
		HTTPAPIHostInvalid,
		HTTPAPIOriginInvalid,
		HTTPAPIBootstrapInvalid,
		HTTPAPISessionInvalid,
		HTTPAPISessionExpired,
		HTTPAPICSRFInvalid,
		HTTPAPIMethodNotAllowed,
		HTTPAPIRouteNotFound,
		HTTPAPIServiceUnavailable,
		StoreOpenFailed,
		StoreIntegrityFailed,
		StoreSchemaIncompatible,
		StoreMigrationFailed,
		StoreMigrationPartial,
		StoreReadFailed,
		StoreWriteFailed,
		StoreDiagnosticWriteFailed,
		StoreBackupFailed,
		StoreRecoveryCandidateInvalid,
		StoreRecoveryCandidateNotFound,
		StoreRecoveryRestoreFailed,
		VaultUnavailable,
		VaultLocked,
		VaultKeyInvalid,
		VaultEnvelopeInvalid,
		VaultEnvelopeUnsupported,
		VaultKeyGenerationMismatch,
		VaultEncryptionFailed:
		return true
	default:
		return false
	}
}
