package telemetry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

var readyPrerequisites = Prerequisites{
	Endpoint: true, PublicSchema: true, PrivacyNotice: true, EventRetention: true,
	AggregateRetention: true, Deletion: true, Reset: true,
}

type memoryRepository struct {
	mu     sync.Mutex
	state  State
	writes int
}

func (repository *memoryRepository) TelemetryState(context.Context) (State, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.state, nil
}

func (repository *memoryRepository) SetTelemetryState(_ context.Context, state State) (State, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.state = state
	repository.writes++
	return state, nil
}

type prerequisitesStub struct {
	mu    sync.Mutex
	value Prerequisites
	err   error
}

func (provider *prerequisitesStub) TelemetryPrerequisites(context.Context) (Prerequisites, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.value, provider.err
}

type fixedClock struct{ value time.Time }

func (clock fixedClock) Now() time.Time { return clock.value }

type sequenceIDs struct {
	mu     sync.Mutex
	values []string
}

func (ids *sequenceIDs) NewInstallationID() (string, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	if len(ids.values) == 0 {
		return "", errors.New("no ID")
	}
	value := ids.values[0]
	ids.values = ids.values[1:]
	return value, nil
}

type recordingTransport struct {
	mu        sync.Mutex
	events    []EventV1
	deletions []string
	err       error
}

type gatedTransport struct {
	started chan struct{}
	release chan struct{}
}

func (transport *gatedTransport) Send(ctx context.Context, _ EventV1) error {
	select {
	case transport.started <- struct{}{}:
	default:
	}
	select {
	case <-transport.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (transport *gatedTransport) DeleteInstallation(ctx context.Context, _ string) error {
	return transport.Send(ctx, EventV1{})
}

func (transport *recordingTransport) Send(_ context.Context, event EventV1) error {
	transport.mu.Lock()
	transport.events = append(transport.events, event)
	err := transport.err
	transport.mu.Unlock()
	return err
}

func (transport *recordingTransport) DeleteInstallation(_ context.Context, installationID string) error {
	transport.mu.Lock()
	transport.deletions = append(transport.deletions, installationID)
	err := transport.err
	transport.mu.Unlock()
	return err
}

func (transport *recordingTransport) count() int {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return len(transport.events)
}

func (transport *recordingTransport) deletionIDs() []string {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return append([]string(nil), transport.deletions...)
}

func newTestService(t *testing.T, repository *memoryRepository, prerequisites *prerequisitesStub, transport Transport, ids *sequenceIDs, capacity int) *Service {
	t.Helper()
	service, err := NewService(context.Background(), ServiceOptions{
		Repository: repository, Prerequisites: prerequisites, Transport: transport,
		Clock: fixedClock{time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}, IDGenerator: ids,
		AppVersion: "1.2.3", OSFamily: OSLinux, Architecture: ArchitectureAMD64,
		QueueCapacity: capacity, AttemptTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func closeService(t *testing.T, service *Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultOffAndPrerequisitesPreventTransportCalls(t *testing.T) {
	repository := &memoryRepository{}
	prerequisites := &prerequisitesStub{}
	transport := &recordingTransport{}
	service := newTestService(t, repository, prerequisites, transport, &sequenceIDs{values: []string{"00112233445566778899aabbccddeeff"}}, 1)

	snapshot, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != StatusUnavailable || snapshot.Schema.Version != SchemaVersion || snapshot.Consent.Enabled {
		t.Fatalf("unexpected default snapshot: %+v", snapshot)
	}
	if service.Record(Record{Feature: FeatureDashboard, Outcome: OutcomeSucceeded, DurationBucket: DurationUnderOneSecond}) {
		t.Fatal("default-off event was queued")
	}
	if _, err := service.Enable(context.Background(), SchemaVersion); !errors.Is(err, ErrPrerequisitesMissing) {
		t.Fatalf("enable error = %v", err)
	}
	closeService(t, service)
	if transport.count() != 0 || repository.writes != 0 {
		t.Fatalf("default-off path caused writes=%d sends=%d", repository.writes, transport.count())
	}
}

func TestEveryOperationalPrerequisiteIsRequired(t *testing.T) {
	tests := []struct {
		name   string
		remove func(*Prerequisites)
	}{
		{"endpoint", func(value *Prerequisites) { value.Endpoint = false }},
		{"public schema", func(value *Prerequisites) { value.PublicSchema = false }},
		{"privacy notice", func(value *Prerequisites) { value.PrivacyNotice = false }},
		{"30-day event retention", func(value *Prerequisites) { value.EventRetention = false }},
		{"13-month aggregate retention", func(value *Prerequisites) { value.AggregateRetention = false }},
		{"deletion", func(value *Prerequisites) { value.Deletion = false }},
		{"reset", func(value *Prerequisites) { value.Reset = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := readyPrerequisites
			test.remove(&value)
			service := newTestService(t, &memoryRepository{}, &prerequisitesStub{value: value}, &recordingTransport{}, &sequenceIDs{values: []string{"00112233445566778899aabbccddeeff"}}, 1)
			if _, err := service.Enable(context.Background(), ConsentSchemaVersion); !errors.Is(err, ErrPrerequisitesMissing) {
				t.Fatalf("enable error = %v", err)
			}
			closeService(t, service)
		})
	}
}

func TestEnableRevokeResetAndPersistence(t *testing.T) {
	repository := &memoryRepository{}
	prerequisites := &prerequisitesStub{value: readyPrerequisites}
	transport := &recordingTransport{}
	ids := &sequenceIDs{values: []string{"00112233445566778899aabbccddeeff", "ffeeddccbbaa99887766554433221100"}}
	service := newTestService(t, repository, prerequisites, transport, ids, 4)

	if _, err := service.Enable(context.Background(), SchemaVersion+1); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("wrong-schema enable error = %v", err)
	}
	enabled, err := service.Enable(context.Background(), SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if enabled.Status != StatusEnabled || enabled.InstallationID != "00112233445566778899aabbccddeeff" || !enabled.Consent.Enabled {
		t.Fatalf("unexpected enabled state: %+v", enabled)
	}
	reset, err := service.ResetInstallationID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reset.InstallationID != "ffeeddccbbaa99887766554433221100" || reset.Status != StatusEnabled {
		t.Fatalf("unexpected reset state: %+v", reset)
	}
	revoked, err := service.Revoke(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if revoked.Status != StatusDisabled || revoked.Consent.Enabled || service.Record(Record{Feature: FeatureUpdates, Outcome: OutcomeSucceeded, DurationBucket: DurationUnder100Milliseconds}) {
		t.Fatalf("unexpected revoked state: %+v", revoked)
	}
	closeService(t, service)
	deletions := transport.deletionIDs()
	if len(deletions) != 2 || deletions[0] != "00112233445566778899aabbccddeeff" || deletions[1] != "ffeeddccbbaa99887766554433221100" {
		t.Fatalf("reset/revoke deletions = %v", deletions)
	}

	second := newTestService(t, repository, prerequisites, transport, &sequenceIDs{}, 1)
	persisted, err := second.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != StatusDisabled || persisted.InstallationID != reset.InstallationID || persisted.Consent.Enabled {
		t.Fatalf("state did not persist: %+v", persisted)
	}
	closeService(t, second)
}

func TestOldConsentSchemaRequiresFreshExplicitConsent(t *testing.T) {
	repository := &memoryRepository{state: State{
		Consent:        Consent{Enabled: true, SchemaVersion: ConsentSchemaVersion + 1, ConsentedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		InstallationID: "00112233445566778899aabbccddeeff",
	}}
	transport := &recordingTransport{}
	service := newTestService(t, repository, &prerequisitesStub{value: readyPrerequisites}, transport, &sequenceIDs{}, 1)
	snapshot, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != StatusDisabled || service.Record(Record{Feature: FeatureDashboard, Outcome: OutcomeSucceeded, DurationBucket: DurationUnderOneSecond}) {
		t.Fatalf("stale consent remained effective: %+v", snapshot)
	}
	closeService(t, service)
	if transport.count() != 0 {
		t.Fatal("stale consent allowed a transport call")
	}
}

func TestQueueDrainsAndTransportFailuresAreIsolated(t *testing.T) {
	repository := &memoryRepository{}
	prerequisites := &prerequisitesStub{value: readyPrerequisites}
	transport := &recordingTransport{err: errors.New("network failed")}
	service := newTestService(t, repository, prerequisites, transport, &sequenceIDs{values: []string{"00112233445566778899aabbccddeeff"}}, 4)
	if _, err := service.Enable(context.Background(), SchemaVersion); err != nil {
		t.Fatal(err)
	}
	record := Record{Feature: FeatureDiagnostics, Outcome: OutcomeFailed, ErrorCode: apperrors.DiagnosticsEventInvalid, DurationBucket: DurationUnderTenSeconds}
	accepted := 0
	deadline := time.Now().Add(time.Second)
	for accepted < 3 && time.Now().Before(deadline) {
		if service.Record(record) {
			accepted++
		} else {
			time.Sleep(time.Millisecond)
		}
	}
	if accepted != 3 {
		t.Fatalf("accepted %d events, want 3", accepted)
	}
	closeService(t, service)
	if transport.count() != 3 {
		t.Fatalf("drained sends = %d, want 3", transport.count())
	}
}

func TestQueueIsBoundedAndRecordNeverWaits(t *testing.T) {
	repository := &memoryRepository{}
	prerequisites := &prerequisitesStub{value: readyPrerequisites}
	transport := &gatedTransport{started: make(chan struct{}, 1), release: make(chan struct{})}
	service := newTestService(t, repository, prerequisites, transport, &sequenceIDs{values: []string{"00112233445566778899aabbccddeeff"}}, 1)
	if _, err := service.Enable(context.Background(), SchemaVersion); err != nil {
		t.Fatal(err)
	}
	record := Record{Feature: FeatureDashboard, Outcome: OutcomeSucceeded, DurationBucket: DurationUnderOneSecond}
	if !service.Record(record) {
		t.Fatal("first event was not queued")
	}
	select {
	case <-transport.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start first delivery")
	}
	if !service.Record(record) {
		t.Fatal("second event did not fill queue")
	}
	started := time.Now()
	if service.Record(record) {
		t.Fatal("event beyond queue bound was accepted")
	}
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("full queue blocked Record")
	}
	close(transport.release)
	closeService(t, service)
}

func TestAttemptTimeoutBoundsBlockingTransport(t *testing.T) {
	repository := &memoryRepository{}
	prerequisites := &prerequisitesStub{value: readyPrerequisites}
	transport := &gatedTransport{started: make(chan struct{}, 1), release: make(chan struct{})}
	service, err := NewService(context.Background(), ServiceOptions{
		Repository: repository, Prerequisites: prerequisites, Transport: transport,
		Clock:       fixedClock{time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)},
		IDGenerator: &sequenceIDs{values: []string{"00112233445566778899aabbccddeeff"}},
		AppVersion:  "1.2.3", OSFamily: OSLinux, Architecture: ArchitectureAMD64,
		QueueCapacity: 1, AttemptTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Enable(context.Background(), ConsentSchemaVersion); err != nil {
		t.Fatal(err)
	}
	if !service.Record(Record{Feature: FeatureDashboard, Outcome: OutcomeSucceeded, DurationBucket: DurationUnderOneSecond}) {
		t.Fatal("event was not queued")
	}
	select {
	case <-transport.started:
	case <-time.After(time.Second):
		t.Fatal("blocking transport did not start")
	}
	started := time.Now()
	closeService(t, service)
	if time.Since(started) > 500*time.Millisecond {
		t.Fatal("attempt timeout did not bound Close")
	}
}

func TestInvalidRecordNeverEntersQueue(t *testing.T) {
	repository := &memoryRepository{}
	prerequisites := &prerequisitesStub{value: readyPrerequisites}
	transport := &recordingTransport{}
	service := newTestService(t, repository, prerequisites, transport, &sequenceIDs{values: []string{"00112233445566778899aabbccddeeff"}}, 1)
	if _, err := service.Enable(context.Background(), SchemaVersion); err != nil {
		t.Fatal(err)
	}
	if service.Record(Record{Feature: "workspace-secret", Outcome: OutcomeSucceeded, DurationBucket: DurationUnderOneSecond}) {
		t.Fatal("invalid record was queued")
	}
	closeService(t, service)
	if transport.count() != 0 {
		t.Fatal("invalid record reached transport")
	}
}
