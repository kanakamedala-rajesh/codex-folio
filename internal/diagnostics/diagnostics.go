// Package diagnostics owns the bounded, redacted operational diagnostic
// contract. It deliberately has no filesystem, database, HTTP, or platform
// dependencies so each external boundary can choose its own sink.
package diagnostics

import (
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
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"

	ComponentCLI         = "cli"
	ComponentLaunch      = "launch"
	ComponentProfile     = "profile"
	ComponentDiagnostics = "diagnostics"
	ComponentHTTPAPI     = "httpapi"
	ComponentPlatform    = "platform"
	ComponentStore       = "store"
	ComponentVault       = "vault"

	OperationCommand           = "command"
	OperationResolvePaths      = "resolve_paths"
	OperationDiscoverOwner     = "discover_owner"
	OperationDiscoverCodex     = "discover_codex"
	OperationProfileSetup      = "profile_setup"
	OperationAcquireOwner      = "acquire_owner"
	OperationOpenStore         = "open_store"
	OperationIntegrityCheck    = "integrity_check"
	OperationMigration         = "migration"
	OperationBackup            = "backup"
	OperationRecoveryVerify    = "recovery_verify"
	OperationRecoveryList      = "recovery_list"
	OperationRecoveryRestore   = "recovery_restore"
	OperationVault             = "vault"
	OperationHTTPBootstrap     = "http_bootstrap"
	OperationHTTPAuthorization = "http_authorization"
	OperationHTTPSession       = "http_session"
	OperationHTTPAsset         = "http_asset"
	OperationHTTPListen        = "http_listen"
	OperationShutdown          = "shutdown"

	StateFailed      = "failed"
	StateInvalid     = "invalid"
	StateUnavailable = "unavailable"
	StateLocked      = "locked"
	StateExpired     = "expired"
	StateReplayed    = "replayed"
	StateContention  = "contention"
	StateRejected    = "rejected"
	StateRunning     = "running"
	StateStopped     = "stopped"

	VaultTierDPAPI         = "dpapi"
	VaultTierKeychain      = "keychain"
	VaultTierSecretService = "secret-service"
	VaultTierPassphrase    = "passphrase"

	MaxEventBytes          = 2048
	DefaultMaxEvents       = 128
	DefaultMaxEncodedBytes = 50 * 1024 * 1024
	DefaultRetention       = 14 * 24 * time.Hour
	MaxRecorderEvents      = 4096
	MaxRecorderBytes       = 50 * 1024 * 1024
	MaxAggregateCount      = 128
	MaxOccurrenceCount     = 1_000_000
)

// Severity is the small fixed severity vocabulary persisted by the store.
type Severity string

// Context contains only bounded operational values. There is intentionally no
// free-form message, path, identity, content, or secret field.
type Context struct {
	Operation     string `json:"operation,omitempty"`
	State         string `json:"state,omitempty"`
	HTTPMethod    string `json:"http_method,omitempty"`
	HTTPStatus    int    `json:"http_status,omitempty"`
	SchemaVersion int    `json:"schema_version,omitempty"`
	VaultTier     string `json:"vault_tier,omitempty"`
}

// Event is the complete structured diagnostic boundary. Error causes are
// intentionally absent; callers retain them locally for control flow only.
type Event struct {
	Time      time.Time `json:"time"`
	Severity  Severity  `json:"severity"`
	Component string    `json:"component"`
	ErrorCode string    `json:"error_code"`
	Context   *Context  `json:"context,omitempty"`
}

// Clock is the time seam used by RecordError.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// NewEvent creates and validates one redacted event. The context is copied so
// callers cannot mutate an accepted event through their input value.
func NewEvent(at time.Time, severity Severity, component, code string, context Context) (Event, error) {
	event := Event{
		Time:      at.UTC(),
		Severity:  severity,
		Component: component,
		ErrorCode: code,
	}
	if !context.empty() {
		copy := context
		event.Context = &copy
	}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	return event, nil
}

// Validate proves that an event contains only the registered, bounded
// vocabulary before it can be emitted or persisted.
func (event Event) Validate() error {
	switch {
	case event.Time.IsZero():
		return invalidEvent("event time is required")
	case !validSeverity(event.Severity):
		return invalidEvent("event severity is not supported")
	case !validComponent(event.Component):
		return invalidEvent("event component is not supported")
	case !apperrors.IsRegistered(event.ErrorCode):
		return invalidEvent("event error code is not registered")
	}
	if event.Context != nil {
		if err := validateContext(*event.Context); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return invalidEvent("event could not be encoded")
	}
	if len(encoded) > MaxEventBytes {
		return invalidEvent("event exceeds the encoded size limit")
	}
	return nil
}

func invalidEvent(reason string) error {
	return apperrors.New(apperrors.DiagnosticsEventInvalid, errors.New(reason))
}

func (context Context) empty() bool {
	return context.Operation == "" && context.State == "" && context.HTTPMethod == "" && context.HTTPStatus == 0 && context.SchemaVersion == 0 && context.VaultTier == ""
}

func validateContext(context Context) error {
	if context.Operation != "" && !validOperation(context.Operation) {
		return invalidEvent("diagnostic operation is not allowlisted")
	}
	if context.State != "" && !validState(context.State) {
		return invalidEvent("diagnostic state is not allowlisted")
	}
	if context.HTTPMethod != "" && !validHTTPMethod(context.HTTPMethod) {
		return invalidEvent("diagnostic HTTP method is not allowlisted")
	}
	if context.HTTPStatus != 0 && (context.HTTPStatus < 100 || context.HTTPStatus > 599) {
		return invalidEvent("diagnostic HTTP status is out of range")
	}
	if context.SchemaVersion < 0 || context.SchemaVersion > 1_000_000 {
		return invalidEvent("diagnostic schema version is out of range")
	}
	if context.VaultTier != "" && !validVaultTier(context.VaultTier) {
		return invalidEvent("diagnostic vault tier is not allowlisted")
	}
	return nil
}

func validSeverity(value Severity) bool {
	return value == SeverityInfo || value == SeverityWarning || value == SeverityError
}

func validComponent(value string) bool {
	switch value {
	case ComponentCLI, ComponentLaunch, ComponentProfile, ComponentDiagnostics, ComponentHTTPAPI, ComponentPlatform, ComponentStore, ComponentVault:
		return true
	default:
		return false
	}
}

func validOperation(value string) bool {
	switch value {
	case OperationCommand, OperationResolvePaths, OperationDiscoverOwner, OperationDiscoverCodex, OperationProfileSetup, OperationAcquireOwner, OperationOpenStore, OperationIntegrityCheck, OperationMigration, OperationBackup, OperationRecoveryVerify, OperationRecoveryList, OperationRecoveryRestore, OperationVault, OperationHTTPBootstrap, OperationHTTPAuthorization, OperationHTTPSession, OperationHTTPAsset, OperationHTTPListen, OperationShutdown:
		return true
	default:
		return false
	}
}

func validState(value string) bool {
	switch value {
	case StateFailed, StateInvalid, StateUnavailable, StateLocked, StateExpired, StateReplayed, StateContention, StateRejected, StateRunning, StateStopped:
		return true
	default:
		return false
	}
}

func validHTTPMethod(value string) bool {
	switch value {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		return true
	default:
		return false
	}
}

func validVaultTier(value string) bool {
	switch value {
	case VaultTierDPAPI, VaultTierKeychain, VaultTierSecretService, VaultTierPassphrase:
		return true
	default:
		return false
	}
}

// CodeFor returns a registered code suitable for a diagnostic event. It
// intentionally discards unregistered coded errors instead of echoing them.
func CodeFor(err error, fallback string) string {
	if code := apperrors.Code(err); apperrors.IsRegistered(code) {
		return code
	}
	if apperrors.IsRegistered(fallback) {
		return fallback
	}
	return apperrors.CLIInternal
}

// ComponentForCode maps registered ownership prefixes to the diagnostic
// component vocabulary without inspecting an error cause.
func ComponentForCode(code string) string {
	switch {
	case strings.HasPrefix(code, "CF_DIAGNOSTICS_"):
		return ComponentDiagnostics
	case strings.HasPrefix(code, "CF_LAUNCH_"):
		return ComponentLaunch
	case strings.HasPrefix(code, "CF_PROFILE_"):
		return ComponentProfile
	case strings.HasPrefix(code, "CF_HTTPAPI_"):
		return ComponentHTTPAPI
	case strings.HasPrefix(code, "CF_PLATFORM_"):
		return ComponentPlatform
	case strings.HasPrefix(code, "CF_STORE_"):
		return ComponentStore
	case strings.HasPrefix(code, "CF_VAULT_"):
		return ComponentVault
	default:
		return ComponentCLI
	}
}

// Sink is the transport-independent diagnostic persistence boundary.
type Sink interface {
	Record(Event) error
}

// SinkFunc adapts a function to Sink for composition and focused tests.
type SinkFunc func(Event) error

func (function SinkFunc) Record(event Event) error { return function(event) }

// RecorderOptions bounds the in-memory event buffer. Zero values select the
// fixed production defaults; values above the hard caps are rejected.
type RecorderOptions struct {
	Clock           Clock
	MaxEvents       int
	MaxEncodedBytes int
	MaxEventBytes   int
	Retention       time.Duration
}

// Recorder retains a bounded recent event projection. It is suitable for
// pre-SQLite failures because it never creates a fallback file or log.
type Recorder struct {
	mu              sync.Mutex
	clock           Clock
	maxEvents       int
	maxEncodedBytes int
	maxEventBytes   int
	retention       time.Duration
	events          []Event
	encodedBytes    int
}

// NewRecorder creates a bounded in-memory recorder.
func NewRecorder(options RecorderOptions) (*Recorder, error) {
	if options.MaxEvents < 0 || options.MaxEvents > MaxRecorderEvents || options.MaxEncodedBytes < 0 || options.MaxEncodedBytes > MaxRecorderBytes || options.MaxEventBytes < 0 || options.MaxEventBytes > MaxEventBytes || options.Retention < 0 {
		return nil, apperrors.New(apperrors.DiagnosticsConfigurationInvalid, errors.New("diagnostic bounds are invalid"))
	}
	maxEvents := options.MaxEvents
	if maxEvents == 0 {
		maxEvents = DefaultMaxEvents
	}
	maxEncodedBytes := options.MaxEncodedBytes
	if maxEncodedBytes == 0 {
		maxEncodedBytes = DefaultMaxEncodedBytes
	}
	maxEventBytes := options.MaxEventBytes
	if maxEventBytes == 0 {
		maxEventBytes = MaxEventBytes
	}
	retention := options.Retention
	if retention == 0 {
		retention = DefaultRetention
	}
	clock := options.Clock
	if clock == nil {
		clock = systemClock{}
	}
	return &Recorder{
		clock:           clock,
		maxEvents:       maxEvents,
		maxEncodedBytes: maxEncodedBytes,
		maxEventBytes:   maxEventBytes,
		retention:       retention,
	}, nil
}

// Record validates and retains one event, evicting the oldest entries as
// necessary. A repeated-error storm therefore remains bounded by both count
// and encoded bytes.
func (recorder *Recorder) Record(event Event) error {
	if recorder == nil {
		return apperrors.New(apperrors.DiagnosticsConfigurationInvalid, errors.New("diagnostic recorder is unavailable"))
	}
	if err := event.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return invalidEvent("event could not be encoded")
	}
	if len(encoded) > recorder.maxEventBytes || len(encoded) > recorder.maxEncodedBytes {
		return invalidEvent("event exceeds the configured size limit")
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	reference := recorder.clock.Now().UTC()
	if reference.IsZero() || reference.Before(event.Time) {
		reference = event.Time
	}
	recorder.removeExpiredLocked(reference)
	if event.Time.Before(reference.Add(-recorder.retention)) {
		return nil
	}
	for len(recorder.events) >= recorder.maxEvents || recorder.encodedBytes+len(encoded) > recorder.maxEncodedBytes {
		recorder.removeOldestLocked()
	}
	recorder.events = append(recorder.events, cloneEvent(event))
	recorder.encodedBytes += len(encoded)
	return nil
}

// RecordError emits only the registered code from err. The cause and its
// message never enter the event projection.
func (recorder *Recorder) RecordError(component, operation string, severity Severity, err error) error {
	if recorder == nil {
		return apperrors.New(apperrors.DiagnosticsConfigurationInvalid, errors.New("diagnostic recorder is unavailable"))
	}
	code := CodeFor(err, apperrors.CLIInternal)
	event, eventErr := NewEvent(recorder.clock.Now(), severity, component, code, Context{Operation: operation})
	if eventErr != nil {
		return eventErr
	}
	return recorder.Record(event)
}

// Events returns a copy of the retained events in chronological order.
func (recorder *Recorder) Events() []Event {
	if recorder == nil {
		return nil
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.removeExpiredLocked(recorder.clock.Now().UTC())
	result := make([]Event, len(recorder.events))
	for index, event := range recorder.events {
		result[index] = cloneEvent(event)
	}
	return result
}

// Len reports the number of retained events.
func (recorder *Recorder) Len() int {
	if recorder == nil {
		return 0
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.removeExpiredLocked(recorder.clock.Now().UTC())
	return len(recorder.events)
}

// EncodedBytes reports the total encoded size retained by the recorder.
func (recorder *Recorder) EncodedBytes() int {
	if recorder == nil {
		return 0
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.removeExpiredLocked(recorder.clock.Now().UTC())
	return recorder.encodedBytes
}

func (recorder *Recorder) removeExpiredLocked(at time.Time) {
	cutoff := at.Add(-recorder.retention)
	kept := recorder.events[:0]
	encodedBytes := 0
	for _, event := range recorder.events {
		if event.Time.Before(cutoff) {
			continue
		}
		kept = append(kept, event)
		if encoded, err := json.Marshal(event); err == nil {
			encodedBytes += len(encoded)
		}
	}
	recorder.events = kept
	recorder.encodedBytes = encodedBytes
}

func (recorder *Recorder) removeOldestLocked() {
	if len(recorder.events) == 0 {
		return
	}
	if encoded, err := json.Marshal(recorder.events[0]); err == nil {
		recorder.encodedBytes -= len(encoded)
		if recorder.encodedBytes < 0 {
			recorder.encodedBytes = 0
		}
	}
	recorder.events = recorder.events[1:]
}

func cloneEvent(event Event) Event {
	if event.Context != nil {
		context := *event.Context
		event.Context = &context
	}
	return event
}

// Aggregate is the persisted redacted projection. It has no event context or
// message and is safe to return through a future local diagnostics view.
type Aggregate struct {
	ID              string    `json:"id"`
	Component       string    `json:"component"`
	ErrorCode       string    `json:"error_code"`
	Severity        Severity  `json:"severity"`
	OccurrenceCount int       `json:"occurrence_count"`
	FirstSeenAt     time.Time `json:"first_seen_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
}

// DiagnosticAggregate is a descriptive alias for callers that prefer the
// complete domain name.
type DiagnosticAggregate = Aggregate

// AggregateID is a deterministic non-sensitive key for one component/code/
// severity bucket. It never hashes context, paths, or error causes.
func AggregateID(event Event) string {
	digest := sha256.Sum256([]byte(event.Component + "\x00" + event.ErrorCode + "\x00" + string(event.Severity)))
	return hex.EncodeToString(digest[:])
}
