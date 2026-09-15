package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/usage"
)

func TestAuthorizedUsageRefreshProjectsNormalizedSnapshot(t *testing.T) {
	capturedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	service := &recordingUsageService{result: usage.Snapshot{
		ID: "snapshot-1", ProfileID: "profile-1", Alias: "Work", Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: capturedAt, Status: usage.AvailabilityPartial,
		TriggerReason: usage.TriggerDashboardOpen,
		Observations:  []usage.Observation{{ID: "observation-1", Metric: usage.Registry()[0], Value: 25, ObservedAt: capturedAt, CapturedAt: capturedAt, CaptureAgeSeconds: 42, WindowTimezone: "UTC", Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable}},
		Availability:  []usage.MetricAvailability{{ID: "availability-1", MetricKey: usage.Registry()[0].Key, State: usage.AvailabilityAvailable, Reason: "", CheckedAt: capturedAt, Provenance: usage.ProvenanceProvider}},
	}}
	server, _, _ := startTestServer(t, Options{Usage: service})
	client := testClient(t)
	origin := server.Origin()
	token := mustBootstrapToken(t, server.BootstrapURL())
	exchange, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+token+`"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	var bootstrap BootstrapResponse
	if err := json.NewDecoder(exchange.Body).Decode(&bootstrap); err != nil {
		t.Fatal(err)
	}
	_ = exchange.Body.Close()

	response, err := doRequest(client, http.MethodPost, origin+UsageRefreshPath, server.Address(), origin, []byte(`{"alias":"Work","trigger_reason":"dashboard_open"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var result UsageSnapshotResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if service.alias != "Work" || service.triggerReason != usage.TriggerDashboardOpen || result.SnapshotId != "snapshot-1" || result.TriggerReason != usage.TriggerDashboardOpen || result.Status != usage.AvailabilityPartial || len(result.Observations) != 1 || result.Observations[0].MetricKey != usage.Registry()[0].Key || result.Observations[0].CaptureAgeSeconds != 42 {
		t.Fatalf("service alias = %q, result = %#v", service.alias, result)
	}
	response, err = doRequest(client, http.MethodPost, origin+UsageRefreshPath, server.Address(), origin, []byte(`{"alias":"Work","trigger_reason":"dashboard_refresh"}`), bootstrap.CSRFToken)
	if err != nil || response.StatusCode != http.StatusOK || service.triggerReason != usage.TriggerDashboardRefresh {
		t.Fatalf("dashboard refresh = status:%d trigger:%q error:%v", response.StatusCode, service.triggerReason, err)
	}
	_ = response.Body.Close()
	response, err = doRequest(client, http.MethodPost, origin+UsageRefreshPath, server.Address(), origin, []byte(`{"alias":"Work"}`), bootstrap.CSRFToken)
	if err != nil || response.StatusCode != http.StatusOK || service.triggerReason != usage.TriggerDashboardRefresh {
		t.Fatalf("legacy dashboard refresh = status:%d trigger:%q error:%v", response.StatusCode, service.triggerReason, err)
	}
	_ = response.Body.Close()
}

func TestUsageRefreshFailureExcludesLastKnownEvidence(t *testing.T) {
	capturedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	service := &recordingUsageService{err: apperrors.New(apperrors.UsageCollectionFailed, errors.New("secret path /home/private and value 87")), result: usage.Snapshot{
		ID: "snapshot-failure", ProfileID: "profile-1", Alias: "Work", Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: capturedAt, Status: usage.AvailabilityTemporarilyUnavailable,
		Observations: []usage.Observation{{Metric: usage.Registry()[0], Value: 0, CapturedAt: capturedAt.Add(-15 * time.Minute), CaptureAgeSeconds: 900, Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessStale, Availability: usage.AvailabilityTemporarilyUnavailable}},
		Availability: []usage.MetricAvailability{{MetricKey: usage.Registry()[0].Key, State: usage.AvailabilityTemporarilyUnavailable, Reason: usage.ReasonCollectionFailed, CheckedAt: capturedAt, Provenance: usage.ProvenanceProvider}},
	}}
	server, _, _ := startTestServer(t, Options{Usage: service, CommandToken: "usage-token"})
	command := NewCommandClient(server.Origin(), "usage-token", nil)
	result, err := command.RefreshUsage(context.Background(), "Work")
	if apperrors.Code(err) != apperrors.UsageCollectionFailed || result.SnapshotId != "" || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "87") {
		t.Fatalf("RefreshUsage() = %#v/%v", result, err)
	}
}

func TestAuthorizedAnalyticsDefaultsToSelectedAndAcceptsExplicitCombinedScope(t *testing.T) {
	capturedAt := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	service := &recordingUsageService{
		view:     usage.DashboardView{Scope: usage.ScopeSelectedProfile, EligibleProfileCount: 2, Profiles: []usage.Snapshot{{ID: "snapshot-1", ProfileID: "profile-1", Alias: "Personal", CapturedAt: capturedAt, Observations: []usage.Observation{}, Availability: []usage.MetricAvailability{}}}},
		activity: []activity.TimelineRecord{{RecordType: activity.RecordTypeManagedLaunch, ID: "launch-1", ProfileID: "profile-1", ProfileAlias: "Personal", Source: "codex_folio", Provenance: activity.ProvenanceManagedLaunch, StartedAt: capturedAt, LastObservedAt: capturedAt, Correlation: activity.Correlation{State: activity.CorrelationUncorrelated}}},
	}
	server, _, _ := startTestServer(t, Options{Usage: service})
	client := testClient(t)
	origin := server.Origin()
	token := mustBootstrapToken(t, server.BootstrapURL())
	exchange, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+token+`"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	var bootstrap BootstrapResponse
	if err := json.NewDecoder(exchange.Body).Decode(&bootstrap); err != nil {
		t.Fatal(err)
	}
	_ = exchange.Body.Close()

	response, err := doRequest(client, http.MethodGet, origin+AnalyticsPath, server.Address(), origin, nil, bootstrap.CSRFToken)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("selected analytics status/error = %d/%v", response.StatusCode, err)
	}
	var selected AnalyticsResponse
	if err := json.NewDecoder(response.Body).Decode(&selected); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if service.scope != "" || selected.Scope != usage.ScopeSelectedProfile || len(selected.Profiles) != 1 || len(selected.Activity) != 1 || selected.Activity[0].CorrelationState != activity.CorrelationUncorrelated {
		t.Fatalf("selected analytics = %#v, service scope %q", selected, service.scope)
	}

	service.view.Scope = usage.ScopeCombinedIdentity
	response, err = doRequest(client, http.MethodGet, origin+AnalyticsPath+"?scope="+usage.ScopeCombinedIdentity, server.Address(), origin, nil, bootstrap.CSRFToken)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("combined analytics status/error = %d/%v", response.StatusCode, err)
	}
	_ = response.Body.Close()
	if service.scope != usage.ScopeCombinedIdentity {
		t.Fatalf("combined service scope = %q", service.scope)
	}
}

type recordingUsageService struct {
	alias         string
	triggerReason string
	result        usage.Snapshot
	view          usage.DashboardView
	activity      []activity.TimelineRecord
	scope         string
	err           error
}

func (service *recordingUsageService) Refresh(_ context.Context, alias, triggerReason string) (usage.Snapshot, error) {
	service.alias = alias
	service.triggerReason = triggerReason
	return service.result, service.err
}

func (service *recordingUsageService) Latest(_ context.Context, alias string) (usage.Snapshot, error) {
	service.alias = alias
	return service.result, nil
}

func (service *recordingUsageService) View(_ context.Context, scope string) (usage.DashboardView, []activity.TimelineRecord, error) {
	service.scope = scope
	return service.view, service.activity, service.err
}

func (service *recordingUsageService) Recent(context.Context, usage.ProfileTarget) ([]usage.Snapshot, error) {
	return []usage.Snapshot{}, nil
}
