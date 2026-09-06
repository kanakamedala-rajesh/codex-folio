package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/usage"
)

func TestAuthorizedUsageRefreshProjectsNormalizedSnapshot(t *testing.T) {
	capturedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	service := &recordingUsageService{result: usage.Snapshot{
		ID: "snapshot-1", ProfileID: "profile-1", Alias: "Work", Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: capturedAt, Status: usage.AvailabilityPartial,
		Observations: []usage.Observation{{ID: "observation-1", Metric: usage.Registry()[0], Value: 25, ObservedAt: capturedAt, CapturedAt: capturedAt, CaptureAgeSeconds: 42, WindowTimezone: "UTC", Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable}},
		Availability: []usage.MetricAvailability{{ID: "availability-1", MetricKey: usage.Registry()[0].Key, State: usage.AvailabilityAvailable, Reason: "", CheckedAt: capturedAt, Provenance: usage.ProvenanceProvider}},
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

	response, err := doRequest(client, http.MethodPost, origin+UsageRefreshPath, server.Address(), origin, []byte(`{"alias":"Work"}`), bootstrap.CSRFToken)
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
	if service.alias != "Work" || result.SnapshotId != "snapshot-1" || result.Status != usage.AvailabilityPartial || len(result.Observations) != 1 || result.Observations[0].MetricKey != usage.Registry()[0].Key || result.Observations[0].CaptureAgeSeconds != 42 {
		t.Fatalf("service alias = %q, result = %#v", service.alias, result)
	}
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

type recordingUsageService struct {
	alias  string
	result usage.Snapshot
	err    error
}

func (service *recordingUsageService) Refresh(_ context.Context, alias string) (usage.Snapshot, error) {
	service.alias = alias
	return service.result, service.err
}

func (service *recordingUsageService) Latest(_ context.Context, alias string) (usage.Snapshot, error) {
	service.alias = alias
	return service.result, nil
}
