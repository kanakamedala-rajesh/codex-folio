// Package apperrors owns stable machine-readable application error identifiers.
package apperrors

import "errors"

const (
	CLIUsage                       = "CF_CLI_USAGE"
	CLIInternal                    = "CF_CLI_INTERNAL"
	PlatformStatePathInvalid       = "CF_PLATFORM_STATE_PATH_INVALID"
	PlatformStatePathUnsafe        = "CF_PLATFORM_STATE_PATH_UNSAFE"
	PlatformPermissionDenied       = "CF_PLATFORM_PERMISSION_DENIED"
	PlatformServiceAlreadyRunning  = "CF_PLATFORM_SERVICE_ALREADY_RUNNING"
	PlatformServiceMetadataInvalid = "CF_PLATFORM_SERVICE_METADATA_INVALID"
	PlatformServiceUnavailable     = "CF_PLATFORM_SERVICE_UNAVAILABLE"
	HTTPAPIHostInvalid             = "CF_HTTPAPI_HOST_INVALID"
	HTTPAPIOriginInvalid           = "CF_HTTPAPI_ORIGIN_INVALID"
	HTTPAPIBootstrapInvalid        = "CF_HTTPAPI_BOOTSTRAP_INVALID"
	HTTPAPISessionInvalid          = "CF_HTTPAPI_SESSION_INVALID"
	HTTPAPISessionExpired          = "CF_HTTPAPI_SESSION_EXPIRED"
	HTTPAPICSRFInvalid             = "CF_HTTPAPI_CSRF_INVALID"
	HTTPAPIMethodNotAllowed        = "CF_HTTPAPI_METHOD_NOT_ALLOWED"
	HTTPAPIRouteNotFound           = "CF_HTTPAPI_ROUTE_NOT_FOUND"
	HTTPAPIServiceUnavailable      = "CF_HTTPAPI_SERVICE_UNAVAILABLE"
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
