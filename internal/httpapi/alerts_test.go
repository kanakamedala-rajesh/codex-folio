package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	alertfeature "venkatasudha.com/codex-folio/internal/alerts"
)

type alertRepositoryStub struct {
	records    []alertfeature.Record
	thresholds []alertfeature.Threshold
	acked      string
	preference alertfeature.NotificationPreference
}

func (repository *alertRepositoryStub) AlertEvidence(context.Context, string) ([]alertfeature.Evidence, error) {
	return nil, nil
}
func (repository *alertRepositoryStub) AlertThresholds(context.Context) ([]alertfeature.Threshold, error) {
	return append([]alertfeature.Threshold(nil), repository.thresholds...), nil
}
func (repository *alertRepositoryStub) SyncAlerts(context.Context, string, []alertfeature.Condition, time.Time, int) error {
	return nil
}
func (repository *alertRepositoryStub) ListAlerts(context.Context, int) ([]alertfeature.Record, error) {
	return append([]alertfeature.Record(nil), repository.records...), nil
}
func (repository *alertRepositoryStub) AcknowledgeAlert(_ context.Context, id string, now time.Time) error {
	repository.acked = id
	for index := range repository.records {
		if repository.records[index].ID == id {
			repository.records[index].State = alertfeature.StateAcknowledged
			repository.records[index].AcknowledgedAt = &now
		}
	}
	return nil
}
func (repository *alertRepositoryStub) SetAlertThreshold(_ context.Context, threshold alertfeature.Threshold, _ time.Time) error {
	repository.thresholds = []alertfeature.Threshold{threshold}
	return nil
}
func (repository *alertRepositoryStub) NotificationPreference(context.Context) (alertfeature.NotificationPreference, error) {
	return repository.preference, nil
}
func (repository *alertRepositoryStub) SetNotificationDetail(_ context.Context, enabled bool, now time.Time) (alertfeature.NotificationPreference, error) {
	repository.preference = alertfeature.NotificationPreference{DetailEnabled: enabled, UpdatedAt: now}
	return repository.preference, nil
}
func (repository *alertRepositoryStub) ClaimAlertDelivery(context.Context, string, time.Time) (bool, error) {
	return true, nil
}
func (repository *alertRepositoryStub) RecordAlertDelivery(context.Context, string, alertfeature.DeliveryOutcome) error {
	return nil
}

type alertClockStub struct{ now time.Time }

func (clock alertClockStub) Now() time.Time { return clock.now }

func TestAlertRecordRetainsNormalizedScopeAndFreshness(t *testing.T) {
	projected := alertRecord(alertfeature.Record{Condition: alertfeature.Condition{
		Scope:     "provider_quota_window",
		Freshness: "fresh",
	}})
	if projected.Scope != "provider_quota_window" || projected.Freshness != "fresh" {
		t.Fatalf("alertRecord() = %#v", projected)
	}
}

func TestBrowserAlertsRequireSessionAndCSRFAndProjectSafeState(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	repository := &alertRepositoryStub{records: []alertfeature.Record{{
		ID: "alert-1", Condition: alertfeature.Condition{ProfileID: "profile-1", ProfileAlias: "Work", Category: alertfeature.CategoryCapacity, Kind: alertfeature.KindCapacityCritical, Severity: alertfeature.SeverityError, Title: "Capacity at critical threshold", Guidance: "Refresh", MetricKey: "codex.primary.used_percent", Scope: "provider_quota_window", Freshness: "fresh", ObservedAt: now},
		State: alertfeature.StateOpen, FirstSeenAt: now, LastSeenAt: now, OccurrenceCount: 2,
	}}}
	service, err := alertfeature.NewService(repository, alertClockStub{now: now})
	if err != nil {
		t.Fatal(err)
	}
	server, _, _ := startTestServer(t, Options{Alerts: service})
	client := testClient(t)
	origin := server.Origin()
	unauthorized, err := doRequest(client, http.MethodGet, origin+AlertsPath, server.Address(), origin, nil, "")
	if err != nil || unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized GET = %d/%v", unauthorized.StatusCode, err)
	}
	_ = unauthorized.Body.Close()
	exchange, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+mustBootstrapToken(t, server.BootstrapURL())+`"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	var bootstrap BootstrapResponse
	if err := json.NewDecoder(exchange.Body).Decode(&bootstrap); err != nil {
		t.Fatal(err)
	}
	_ = exchange.Body.Close()

	read, err := doRequest(client, http.MethodGet, origin+AlertsPath, server.Address(), origin, nil, "")
	if err != nil || read.StatusCode != http.StatusOK {
		t.Fatalf("GET alerts = %d/%v", read.StatusCode, err)
	}
	var result AlertsResponse
	if err := json.NewDecoder(read.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	_ = read.Body.Close()
	if len(result.Active) != 1 || result.Active[0].ProfileAlias != "Work" || result.Active[0].Scope != "provider_quota_window" || result.Active[0].Freshness != "fresh" || result.Active[0].OccurrenceCount != 2 || result.DeliveryHealth.NativeNotifications != "not_enrolled" || result.DeliveryHealth.DetailedContentEnabled {
		t.Fatalf("alerts response = %#v", result)
	}

	withoutCSRF, err := doRequest(client, http.MethodPost, origin+AlertsPath, server.Address(), origin, []byte(`{"action":"acknowledge","alert_id":"alert-1"}`), "")
	if err != nil || withoutCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("POST without CSRF = %d/%v", withoutCSRF.StatusCode, err)
	}
	_ = withoutCSRF.Body.Close()
	acknowledged, err := doRequest(client, http.MethodPost, origin+AlertsPath, server.Address(), origin, []byte(`{"action":"acknowledge","alert_id":"alert-1"}`), bootstrap.CSRFToken)
	if err != nil || acknowledged.StatusCode != http.StatusOK || repository.acked != "alert-1" {
		t.Fatalf("acknowledge = %d/%v/%q", acknowledged.StatusCode, err, repository.acked)
	}
	_ = acknowledged.Body.Close()
	threshold, err := doRequest(client, http.MethodPost, origin+AlertsPath, server.Address(), origin, []byte(`{"action":"set_threshold","profile_id":"profile-1","metric_key":"codex.primary.used_percent","warning_percent":25,"critical_percent":12}`), bootstrap.CSRFToken)
	if err != nil || threshold.StatusCode != http.StatusOK || len(repository.thresholds) != 1 || repository.thresholds[0].WarningPercent != 25 {
		t.Fatalf("threshold = %d/%v/%#v", threshold.StatusCode, err, repository.thresholds)
	}
	_ = threshold.Body.Close()
	privacy, err := doRequest(client, http.MethodPost, origin+AlertsPath, server.Address(), origin, []byte(`{"action":"set_notification_detail","detailed_content_enabled":true}`), bootstrap.CSRFToken)
	if err != nil || privacy.StatusCode != http.StatusOK || !repository.preference.DetailEnabled {
		t.Fatalf("notification detail = %d/%v/%#v", privacy.StatusCode, err, repository.preference)
	}
	var privacyResult AlertsResponse
	if err := json.NewDecoder(privacy.Body).Decode(&privacyResult); err != nil {
		t.Fatal(err)
	}
	_ = privacy.Body.Close()
	if !privacyResult.DeliveryHealth.DetailedContentEnabled {
		t.Fatalf("notification detail response = %#v", privacyResult.DeliveryHealth)
	}
}
