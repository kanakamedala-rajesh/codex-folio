package alerts

import (
	"context"
	"testing"
	"time"
)

type serviceRepository struct {
	evidence   []Evidence
	thresholds []Threshold
	records    []Record
	synced     []Condition
	acked      string
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

type serviceClock struct{ now time.Time }

func (clock serviceClock) Now() time.Time { return clock.now }

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
