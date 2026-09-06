package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/usage"
)

func TestAuthorizedUsageRefreshProjectsNormalizedSnapshot(t *testing.T) {
	capturedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	service := &recordingUsageService{result: usage.Snapshot{
		ID: "snapshot-1", ProfileID: "profile-1", Alias: "Work", Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: capturedAt,
		Observations: []usage.Observation{{ID: "observation-1", Metric: usage.Registry()[0], Value: 25, ObservedAt: capturedAt, Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable}},
		Availability: []usage.MetricAvailability{{ID: "availability-1", MetricKey: usage.Registry()[0].Key, State: usage.AvailabilityAvailable, CheckedAt: capturedAt, Provenance: usage.ProvenanceProvider}},
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
	if service.alias != "Work" || result.SnapshotId != "snapshot-1" || len(result.Observations) != 1 || result.Observations[0].MetricKey != usage.Registry()[0].Key {
		t.Fatalf("service alias = %q, result = %#v", service.alias, result)
	}
}

type recordingUsageService struct {
	alias  string
	result usage.Snapshot
}

func (service *recordingUsageService) Refresh(_ context.Context, alias string) (usage.Snapshot, error) {
	service.alias = alias
	return service.result, nil
}
