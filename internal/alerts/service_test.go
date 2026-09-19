package alerts

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type serviceRepository struct {
	evidence   []Evidence
	thresholds []Threshold
	records    []Record
	synced     []Condition
	acked      string
	preference NotificationPreference
	claimed    []string
	outcomes   []DeliveryOutcome
}

func (repository *serviceRepository) AlertEvidence(context.Context, string) ([]Evidence, error) {
	return repository.evidence, nil
}
func (repository *serviceRepository) AlertThresholds(context.Context) ([]Threshold, error) {
	return repository.thresholds, nil
}
func (repository *serviceRepository) SyncAlerts(_ context.Context, _ string, conditions []Condition, _ time.Time, _ int) error {
	repository.synced = append([]Condition(nil), conditions...)
	return nil
}
func (repository *serviceRepository) ListAlerts(context.Context, int) ([]Record, error) {
	return append([]Record(nil), repository.records...), nil
}
func (repository *serviceRepository) AcknowledgeAlert(_ context.Context, id string, _ time.Time) error {
	repository.acked = id
	return nil
}
func (repository *serviceRepository) SetAlertThreshold(_ context.Context, threshold Threshold, _ time.Time) error {
	repository.thresholds = []Threshold{threshold}
	return nil
}
func (repository *serviceRepository) NotificationPreference(context.Context) (NotificationPreference, error) {
	return repository.preference, nil
}
func (repository *serviceRepository) SetNotificationDetail(_ context.Context, enabled bool, now time.Time) (NotificationPreference, error) {
	repository.preference = NotificationPreference{DetailEnabled: enabled, UpdatedAt: now}
	return repository.preference, nil
}
func (repository *serviceRepository) ClaimAlertDelivery(_ context.Context, id string, _ time.Time) (bool, error) {
	repository.claimed = append(repository.claimed, id)
	for index := range repository.records {
		if repository.records[index].ID == id && repository.records[index].DeliveryState != DeliveryDelivered && repository.records[index].DeliveryState != DeliveryAttempting {
			repository.records[index].DeliveryState = DeliveryAttempting
			repository.records[index].DeliveryAttempts++
			return true, nil
		}
	}
	return false, nil
}
func (repository *serviceRepository) RecordAlertDelivery(_ context.Context, id string, outcome DeliveryOutcome) error {
	repository.outcomes = append(repository.outcomes, outcome)
	for index := range repository.records {
		if repository.records[index].ID == id {
			repository.records[index].DeliveryState = outcome.State
			repository.records[index].DeliveryAttempts = outcome.Attempts
			repository.records[index].NextDeliveryAttemptAt = outcome.NextAttemptAt
			repository.records[index].DeliveredAt = outcome.DeliveredAt
			repository.records[index].DeliveryErrorCode = outcome.ErrorCode
		}
	}
	return nil
}

type serviceClock struct{ now time.Time }

func (clock serviceClock) Now() time.Time { return clock.now }

type recordingNotificationAdapter struct {
	health        AdapterHealth
	notifications []Notification
	err           error
	waitForCancel bool
	deadlines     []time.Duration
}

func (adapter *recordingNotificationAdapter) Health(context.Context) AdapterHealth {
	return adapter.health
}
func (adapter *recordingNotificationAdapter) Deliver(ctx context.Context, notification Notification) error {
	adapter.notifications = append(adapter.notifications, notification)
	if deadline, ok := ctx.Deadline(); ok {
		adapter.deadlines = append(adapter.deadlines, time.Until(deadline))
	}
	if adapter.waitForCancel {
		<-ctx.Done()
		return ctx.Err()
	}
	return adapter.err
}

func TestServiceEvaluateAndAcknowledge(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	repository := &serviceRepository{evidence: []Evidence{{ProfileID: "profile-1", Alias: "Work", ProfileStatus: "needs_reauthentication"}}, records: []Record{{ID: "alert-1", State: StateOpen}}}
	service, err := NewService(repository, serviceClock{now: now})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	records, err := service.Evaluate(context.Background(), "")
	if err != nil || len(records) != 1 || len(repository.synced) != 1 || repository.synced[0].Kind != KindReauthentication {
		t.Fatalf("Evaluate() = %#v/%v, synced %#v", records, err, repository.synced)
	}
	if _, err := service.Acknowledge(context.Background(), "alert-1"); err != nil || repository.acked != "alert-1" {
		t.Fatalf("Acknowledge() error = %v, acked %q", err, repository.acked)
	}
}

func TestServiceRejectsInvalidThreshold(t *testing.T) {
	service, err := NewService(&serviceRepository{}, serviceClock{now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	for _, threshold := range []Threshold{
		{ProfileID: "", MetricKey: "codex.primary.used_percent", WarningPercent: 20, CriticalPercent: 10},
		{ProfileID: "profile-1", MetricKey: "codex.primary.used_percent", WarningPercent: 10, CriticalPercent: 20},
		{ProfileID: "profile-1", MetricKey: "unknown", WarningPercent: 20, CriticalPercent: 10},
	} {
		if _, err := service.SetThreshold(context.Background(), threshold); err == nil {
			t.Fatalf("SetThreshold(%#v) succeeded", threshold)
		}
	}
}

func TestServiceDeliversGenericContentOnceAndDetailedContentOnlyAfterConsent(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	remaining := 8.0
	record := Record{ID: "alert-1", State: StateOpen, DeliveryState: DeliveryPending, Condition: Condition{ProfileAlias: "Private profile", Title: "Capacity critical", Guidance: "Switch profiles", RemainingPercent: &remaining}}
	repository := &serviceRepository{records: []Record{record}}
	adapter := &recordingNotificationAdapter{health: AdapterHealth{Available: true, Mechanism: "recording", Detail: "available"}}
	service, err := NewService(repository, serviceClock{now: now}, DeliveryOptions{Enabled: true, Adapter: adapter})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Evaluate(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(adapter.notifications) != 1 || strings.Contains(adapter.notifications[0].Title+adapter.notifications[0].Body, "Private profile") || strings.Contains(adapter.notifications[0].Title+adapter.notifications[0].Body, "8.0") {
		t.Fatalf("generic notification = %#v", adapter.notifications)
	}
	if _, err := service.Evaluate(context.Background(), ""); err != nil || len(adapter.notifications) != 1 {
		t.Fatalf("deduplicated Evaluate() error/count = %v/%d", err, len(adapter.notifications))
	}

	repository.records = []Record{record}
	if _, err := service.SetNotificationDetail(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Evaluate(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(adapter.notifications) != 2 || !strings.Contains(adapter.notifications[1].Body, "Private profile") || !strings.Contains(adapter.notifications[1].Body, "8.0% remaining") {
		t.Fatalf("detailed notification = %#v", adapter.notifications)
	}
	if _, err := service.SetNotificationDetail(context.Background(), false); err != nil || repository.preference.DetailEnabled {
		t.Fatalf("revoke detail = %#v/%v", repository.preference, err)
	}
}

func TestServiceBoundsDeliveryFailureWithoutFailingAlertEvaluation(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	repository := &serviceRepository{records: []Record{{ID: "alert-1", State: StateOpen, DeliveryState: DeliveryPending}}}
	adapter := &recordingNotificationAdapter{health: AdapterHealth{Available: true, Mechanism: "recording", Detail: "available"}, err: errors.New("injected delivery failure")}
	service, err := NewService(repository, serviceClock{now: now}, DeliveryOptions{Enabled: true, Adapter: adapter})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Evaluate(context.Background(), ""); err != nil {
		t.Fatalf("delivery failure escaped Evaluate(): %v", err)
	}
	if got := repository.records[0]; got.DeliveryState != DeliveryFailed || got.DeliveryAttempts != 1 || got.DeliveryErrorCode != DeliveryErrorCode || got.NextDeliveryAttemptAt == nil {
		t.Fatalf("failed delivery state = %#v", got)
	}
	service.clock = serviceClock{now: now.Add(30 * time.Second)}
	_, _ = service.Evaluate(context.Background(), "")
	if len(adapter.notifications) != 1 {
		t.Fatalf("delivery retried before backoff: %d", len(adapter.notifications))
	}
	for _, advance := range []time.Duration{time.Minute, 6 * time.Minute, 12 * time.Minute} {
		service.clock = serviceClock{now: now.Add(advance)}
		_, _ = service.Evaluate(context.Background(), "")
	}
	if len(adapter.notifications) != DeliveryMaxAttempts || repository.records[0].DeliveryAttempts != DeliveryMaxAttempts || repository.records[0].NextDeliveryAttemptAt != nil {
		t.Fatalf("bounded attempts = %d, record %#v", len(adapter.notifications), repository.records[0])
	}
	health, err := service.DeliveryHealth(context.Background())
	if err != nil || health.Native != DeliveryFailed {
		t.Fatalf("DeliveryHealth() = %#v/%v", health, err)
	}
}

func TestServiceBoundsHungDeliveryAcrossAlertBatch(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	repository := &serviceRepository{records: []Record{
		{ID: "alert-1", State: StateOpen, DeliveryState: DeliveryPending},
		{ID: "alert-2", State: StateOpen, DeliveryState: DeliveryPending},
		{ID: "alert-3", State: StateOpen, DeliveryState: DeliveryPending},
	}}
	adapter := &recordingNotificationAdapter{health: AdapterHealth{Available: true, Mechanism: "recording", Detail: "available"}, waitForCancel: true}
	service, err := NewService(repository, serviceClock{now: now}, DeliveryOptions{Enabled: true, Adapter: adapter})
	if err != nil {
		t.Fatal(err)
	}
	service.deliveryBudget = 100 * time.Millisecond
	service.deliveryAttemptBudget = 25 * time.Millisecond
	service.deliveryRecordReserve = 25 * time.Millisecond
	started := time.Now()
	if _, err := service.Evaluate(context.Background(), ""); err != nil {
		t.Fatalf("hung delivery escaped Evaluate(): %v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("Evaluate() took %v with a 100ms delivery budget", elapsed)
	}
	if len(adapter.notifications) == 0 || len(adapter.notifications) >= len(repository.records) {
		t.Fatalf("bounded delivery attempts = %d, records = %d", len(adapter.notifications), len(repository.records))
	}
	for index, deadline := range adapter.deadlines {
		if deadline <= 0 || deadline > service.deliveryAttemptBudget {
			t.Fatalf("delivery deadline %d = %v, budget = %v", index, deadline, service.deliveryAttemptBudget)
		}
	}
	for index := range adapter.notifications {
		if got := repository.records[index]; got.DeliveryState != DeliveryFailed || got.DeliveryAttempts != 1 || got.DeliveryErrorCode != DeliveryErrorCode {
			t.Fatalf("timed-out record %d = %#v", index, got)
		}
	}
}

func TestServiceReportsEnrollmentAndFacilityAvailabilitySeparately(t *testing.T) {
	repository := &serviceRepository{}
	adapter := &recordingNotificationAdapter{health: AdapterHealth{Mechanism: "recording", Detail: "facility missing"}}
	notEnrolled, _ := NewService(repository, serviceClock{now: time.Now()}, DeliveryOptions{Adapter: adapter})
	health, err := notEnrolled.DeliveryHealth(context.Background())
	if err != nil || health.Native != "not_enrolled" {
		t.Fatalf("not-enrolled health = %#v/%v", health, err)
	}
	enrolled, _ := NewService(repository, serviceClock{now: time.Now()}, DeliveryOptions{Enabled: true, Adapter: adapter})
	health, err = enrolled.DeliveryHealth(context.Background())
	if err != nil || health.Native != DeliveryUnavailable || health.Mechanism != "recording" {
		t.Fatalf("unavailable health = %#v/%v", health, err)
	}
}
