package telemetry

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	DefaultQueueCapacity  = 32
	MaximumQueueCapacity  = 256
	DefaultAttemptTimeout = 2 * time.Second
)

type Consent struct {
	Enabled       bool      `json:"enabled"`
	SchemaVersion int       `json:"schema_version,omitempty"`
	ConsentedAt   time.Time `json:"consented_at,omitempty"`
}

type State struct {
	Consent        Consent `json:"consent"`
	InstallationID string  `json:"installation_id"`
}

type Repository interface {
	TelemetryState(context.Context) (State, error)
	SetTelemetryState(context.Context, State) (State, error)
}

type Prerequisites struct {
	Endpoint           bool `json:"endpoint"`
	PublicSchema       bool `json:"public_schema"`
	PrivacyNotice      bool `json:"privacy_notice"`
	EventRetention     bool `json:"event_retention"`
	AggregateRetention bool `json:"aggregate_retention"`
	Deletion           bool `json:"deletion"`
	Reset              bool `json:"reset"`
}

func (value Prerequisites) Ready() bool {
	return value.Endpoint && value.PublicSchema && value.PrivacyNotice && value.EventRetention &&
		value.AggregateRetention && value.Deletion && value.Reset
}

type PrerequisiteProvider interface {
	TelemetryPrerequisites(context.Context) (Prerequisites, error)
}

type Transport interface {
	Send(context.Context, EventV1) error
	DeleteInstallation(context.Context, string) error
}

type Clock interface{ Now() time.Time }

type IDGenerator interface{ NewInstallationID() (string, error) }

type Status string

const (
	StatusUnavailable Status = "unavailable"
	StatusDisabled    Status = "disabled"
	StatusEnabled     Status = "enabled"
)

type Snapshot struct {
	Status         Status        `json:"status"`
	Schema         PublicSchema  `json:"schema"`
	Prerequisites  Prerequisites `json:"prerequisites"`
	Consent        Consent       `json:"consent"`
	InstallationID string        `json:"installation_id"`
}

type Record struct {
	Feature        Feature
	Outcome        Outcome
	ErrorCode      string
	DurationBucket DurationBucket
}

type ServiceOptions struct {
	Repository     Repository
	Prerequisites  PrerequisiteProvider
	Transport      Transport
	Clock          Clock
	IDGenerator    IDGenerator
	AppVersion     string
	OSFamily       OSFamily
	Architecture   Architecture
	QueueCapacity  int
	AttemptTimeout time.Duration
}

type queuedEvent struct {
	event EventV1
	epoch uint64
}

type Service struct {
	repository     Repository
	prerequisites  PrerequisiteProvider
	transport      Transport
	clock          Clock
	ids            IDGenerator
	appVersion     string
	osFamily       OSFamily
	architecture   Architecture
	attemptTimeout time.Duration

	mu                sync.RWMutex
	state             State
	prerequisiteState Prerequisites
	epoch             uint64
	closed            bool
	attemptCancel     context.CancelFunc
	eventCancel       context.CancelFunc
	queue             chan queuedEvent
	privacyQueue      chan string
	stop              chan struct{}
	done              chan struct{}
	closeOnce         sync.Once
}

func NewService(ctx context.Context, options ServiceOptions) (*Service, error) {
	if options.Repository == nil || options.Prerequisites == nil || options.Transport == nil || options.Clock == nil || options.IDGenerator == nil ||
		!validAppVersion(options.AppVersion) || !validOS(options.OSFamily) || !validArchitecture(options.Architecture) {
		return nil, ErrInvalid
	}
	capacity := options.QueueCapacity
	if capacity == 0 {
		capacity = DefaultQueueCapacity
	}
	if capacity < 1 || capacity > MaximumQueueCapacity {
		return nil, ErrInvalid
	}
	timeout := options.AttemptTimeout
	if timeout == 0 {
		timeout = DefaultAttemptTimeout
	}
	if timeout < 0 {
		return nil, ErrInvalid
	}
	state, err := options.Repository.TelemetryState(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateState(state); err != nil {
		return nil, err
	}
	prerequisites, prerequisiteErr := options.Prerequisites.TelemetryPrerequisites(ctx)
	if prerequisiteErr != nil {
		prerequisites = Prerequisites{}
	}
	service := &Service{
		repository: options.Repository, prerequisites: options.Prerequisites, transport: options.Transport,
		clock: options.Clock, ids: options.IDGenerator, appVersion: options.AppVersion,
		osFamily: options.OSFamily, architecture: options.Architecture, attemptTimeout: timeout,
		state: state, prerequisiteState: prerequisites, queue: make(chan queuedEvent, capacity),
		privacyQueue: make(chan string, capacity),
		stop:         make(chan struct{}), done: make(chan struct{}),
	}
	go service.run()
	return service, nil
}

func (service *Service) Status(ctx context.Context) (Snapshot, error) {
	if service == nil {
		return Snapshot{}, ErrInvalid
	}
	prerequisites, err := service.prerequisites.TelemetryPrerequisites(ctx)
	if err != nil {
		prerequisites = Prerequisites{}
	}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return Snapshot{}, ErrClosed
	}
	service.prerequisiteState = prerequisites
	snapshot := service.snapshotLocked()
	service.mu.Unlock()
	return snapshot, nil
}

// Enable accepts only the current public schema version. This argument is a
// dedicated telemetry confirmation and must not be reused from another flow.
func (service *Service) Enable(ctx context.Context, confirmedConsentSchemaVersion int) (Snapshot, error) {
	if service == nil || confirmedConsentSchemaVersion != ConsentSchemaVersion {
		return Snapshot{}, ErrConsentRequired
	}
	prerequisites, err := service.prerequisites.TelemetryPrerequisites(ctx)
	if err != nil || !prerequisites.Ready() {
		return Snapshot{}, ErrPrerequisitesMissing
	}
	now := service.clock.Now().UTC()
	if now.IsZero() {
		return Snapshot{}, ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		return Snapshot{}, ErrClosed
	}
	state := service.state
	if state.InstallationID == "" {
		state.InstallationID, err = service.ids.NewInstallationID()
		if err != nil || !installationIDRegexp.MatchString(state.InstallationID) {
			return Snapshot{}, ErrInvalid
		}
	}
	state.Consent = Consent{Enabled: true, SchemaVersion: ConsentSchemaVersion, ConsentedAt: now}
	state, err = service.repository.SetTelemetryState(ctx, state)
	if err != nil {
		return Snapshot{}, err
	}
	if err := validateState(state); err != nil {
		return Snapshot{}, err
	}
	service.state = state
	service.prerequisiteState = prerequisites
	service.epoch++
	return service.snapshotLocked(), nil
}

func (service *Service) Revoke(ctx context.Context) (Snapshot, error) {
	if service == nil {
		return Snapshot{}, ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		return Snapshot{}, ErrClosed
	}
	state := service.state
	oldID := state.InstallationID
	state.Consent = Consent{}
	state, err := service.repository.SetTelemetryState(ctx, state)
	if err != nil {
		return Snapshot{}, err
	}
	service.state = state
	service.epoch++
	if service.eventCancel != nil {
		service.eventCancel()
		service.eventCancel = nil
	}
	service.queueDeletionLocked(oldID)
	return service.snapshotLocked(), nil
}

func (service *Service) ResetInstallationID(ctx context.Context) (Snapshot, error) {
	if service == nil {
		return Snapshot{}, ErrInvalid
	}
	newID, err := service.ids.NewInstallationID()
	if err != nil || !installationIDRegexp.MatchString(newID) {
		return Snapshot{}, ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		return Snapshot{}, ErrClosed
	}
	state := service.state
	oldID := state.InstallationID
	state.InstallationID = newID
	state, err = service.repository.SetTelemetryState(ctx, state)
	if err != nil {
		return Snapshot{}, err
	}
	service.state = state
	service.epoch++
	if service.eventCancel != nil {
		service.eventCancel()
		service.eventCancel = nil
	}
	service.queueDeletionLocked(oldID)
	return service.snapshotLocked(), nil
}

// Record queues a closed-schema event without waiting for prerequisite or
// transport I/O. False means disabled, unavailable, full, or closed.
func (service *Service) Record(record Record) bool {
	if service == nil {
		return false
	}
	if !service.mu.TryRLock() {
		return false
	}
	if service.closed || !service.state.Consent.Enabled || service.state.Consent.SchemaVersion != ConsentSchemaVersion || !service.prerequisiteState.Ready() {
		service.mu.RUnlock()
		return false
	}
	event := EventV1{
		SchemaVersion: SchemaVersion, AppVersion: service.appVersion, OSFamily: service.osFamily,
		Architecture: service.architecture, Feature: record.Feature, Outcome: record.Outcome,
		ErrorCode: record.ErrorCode, DurationBucket: record.DurationBucket, InstallationID: service.state.InstallationID,
	}
	epoch := service.epoch
	if event.Validate() != nil {
		service.mu.RUnlock()
		return false
	}
	select {
	case service.queue <- queuedEvent{event: event, epoch: epoch}:
		service.mu.RUnlock()
		return true
	default:
		service.mu.RUnlock()
		return false
	}
}

func (service *Service) Close(ctx context.Context) error {
	if service == nil {
		return ErrInvalid
	}
	service.closeOnce.Do(func() {
		service.mu.Lock()
		service.closed = true
		service.mu.Unlock()
		close(service.stop)
	})
	select {
	case <-service.done:
		return nil
	case <-ctx.Done():
		service.mu.Lock()
		if service.attemptCancel != nil {
			service.attemptCancel()
		}
		service.mu.Unlock()
		return ctx.Err()
	}
}

func (service *Service) run() {
	defer close(service.done)
	for {
		select {
		case item := <-service.queue:
			service.sendEvent(item)
		case installationID := <-service.privacyQueue:
			service.sendDeletion(installationID)
		case <-service.stop:
			for {
				select {
				case item := <-service.queue:
					service.sendEvent(item)
				case installationID := <-service.privacyQueue:
					service.sendDeletion(installationID)
				default:
					return
				}
			}
		}
	}
}

func (service *Service) sendEvent(item queuedEvent) {
	service.mu.Lock()
	if !service.state.Consent.Enabled || service.epoch != item.epoch || !service.prerequisiteState.Ready() {
		service.mu.Unlock()
		return
	}
	attemptCtx, cancel := context.WithTimeout(context.Background(), service.attemptTimeout)
	service.attemptCancel = cancel
	service.eventCancel = cancel
	service.mu.Unlock()

	prerequisites, err := service.prerequisites.TelemetryPrerequisites(attemptCtx)
	if err == nil && prerequisites.Ready() {
		_ = service.transport.Send(attemptCtx, item.event)
	}
	cancel()
	service.mu.Lock()
	service.attemptCancel = nil
	service.eventCancel = nil
	service.prerequisiteState = prerequisites
	service.mu.Unlock()
}

func (service *Service) sendDeletion(installationID string) {
	if !installationIDRegexp.MatchString(installationID) {
		return
	}
	service.mu.Lock()
	if !service.prerequisiteState.Ready() {
		service.mu.Unlock()
		return
	}
	attemptCtx, cancel := context.WithTimeout(context.Background(), service.attemptTimeout)
	service.attemptCancel = cancel
	service.mu.Unlock()

	prerequisites, err := service.prerequisites.TelemetryPrerequisites(attemptCtx)
	if err == nil && prerequisites.Ready() {
		_ = service.transport.DeleteInstallation(attemptCtx, installationID)
	}
	cancel()
	service.mu.Lock()
	service.attemptCancel = nil
	service.prerequisiteState = prerequisites
	service.mu.Unlock()
}

// queueDeletionLocked is deliberately best effort and bounded. Local revoke
// and reset state is committed first and never depends on remote availability.
func (service *Service) queueDeletionLocked(installationID string) {
	if installationID == "" || !service.prerequisiteState.Ready() {
		return
	}
	select {
	case service.privacyQueue <- installationID:
	default:
	}
}

func (service *Service) snapshotLocked() Snapshot {
	status := StatusUnavailable
	if service.prerequisiteState.Ready() {
		status = StatusDisabled
		if service.state.Consent.Enabled && service.state.Consent.SchemaVersion == ConsentSchemaVersion {
			status = StatusEnabled
		}
	}
	return Snapshot{Status: status, Schema: Schema(), Prerequisites: service.prerequisiteState, Consent: service.state.Consent, InstallationID: service.state.InstallationID}
}

func validateState(state State) error {
	if state.InstallationID != "" && !installationIDRegexp.MatchString(state.InstallationID) {
		return ErrInvalid
	}
	if state.Consent.Enabled {
		if state.Consent.SchemaVersion < 1 || state.Consent.ConsentedAt.IsZero() || state.InstallationID == "" {
			return ErrInvalid
		}
	} else if state.Consent.SchemaVersion != 0 || !state.Consent.ConsentedAt.IsZero() {
		return ErrInvalid
	}
	return nil
}

// ValidateState applies the durable consent and identifier invariants at
// repository boundaries as well as inside the service.
func ValidateState(state State) error { return validateState(state) }

func IsUnavailable(err error) bool {
	return errors.Is(err, ErrPrerequisitesMissing) || errors.Is(err, ErrTransportDisabled)
}
