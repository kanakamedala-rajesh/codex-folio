package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/telemetry"
)

type telemetryRepositoryStub struct{ state telemetry.State }

func (repository *telemetryRepositoryStub) TelemetryState(context.Context) (telemetry.State, error) {
	return repository.state, nil
}
func (repository *telemetryRepositoryStub) SetTelemetryState(_ context.Context, state telemetry.State) (telemetry.State, error) {
	repository.state = state
	return state, nil
}

type telemetryPrerequisitesStub struct{ value telemetry.Prerequisites }

func (provider telemetryPrerequisitesStub) TelemetryPrerequisites(context.Context) (telemetry.Prerequisites, error) {
	return provider.value, nil
}

type telemetryIDStub struct{ values []string }

type telemetryTransportStub struct{}

func (telemetryTransportStub) Send(context.Context, telemetry.EventV1) error    { return nil }
func (telemetryTransportStub) DeleteInstallation(context.Context, string) error { return nil }

func (ids *telemetryIDStub) NewInstallationID() (string, error) {
	value := ids.values[0]
	ids.values = ids.values[1:]
	return value, nil
}

func TestBrowserTelemetryRequiresExactConsentAndNeverExposesInstallationID(t *testing.T) {
	repository := &telemetryRepositoryStub{}
	service, err := telemetry.NewService(context.Background(), telemetry.ServiceOptions{
		Repository:    repository,
		Prerequisites: telemetryPrerequisitesStub{value: telemetry.Prerequisites{Endpoint: true, PublicSchema: true, PrivacyNotice: true, EventRetention: true, AggregateRetention: true, Deletion: true, Reset: true}},
		Transport:     telemetryTransportStub{}, Clock: alertClockStub{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)},
		IDGenerator: &telemetryIDStub{values: []string{"00112233445566778899aabbccddeeff", "ffeeddccbbaa99887766554433221100"}},
		AppVersion:  "0.0.1-alpha", OSFamily: telemetry.OSLinux, Architecture: telemetry.ArchitectureAMD64,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	server, _, _ := startTestServer(t, Options{Telemetry: service})
	client := testClient(t)
	origin := server.Origin()
	exchange, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+mustBootstrapToken(t, server.BootstrapURL())+`"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	var bootstrap BootstrapResponse
	if err := json.NewDecoder(exchange.Body).Decode(&bootstrap); err != nil {
		t.Fatal(err)
	}
	_ = exchange.Body.Close()

	invalid, err := doRequest(client, http.MethodPost, origin+TelemetryPath, server.Address(), origin, []byte(`{"action":"enable","schema_version":2}`), bootstrap.CSRFToken)
	if err != nil || invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid consent = %d/%v", invalid.StatusCode, err)
	}
	_ = invalid.Body.Close()
	enabled, err := doRequest(client, http.MethodPost, origin+TelemetryPath, server.Address(), origin, []byte(`{"action":"enable","schema_version":1}`), bootstrap.CSRFToken)
	if err != nil || enabled.StatusCode != http.StatusOK {
		t.Fatalf("enable = %d/%v", enabled.StatusCode, err)
	}
	var body map[string]any
	if err := json.NewDecoder(enabled.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	_ = enabled.Body.Close()
	if body["enabled"] != true || body["installation_id_present"] != true {
		t.Fatalf("response = %#v", body)
	}
	for key := range body {
		if strings.Contains(key, "installation_id") && key != "installation_id_present" {
			t.Fatalf("response exposed installation identifier field %q", key)
		}
	}

	reset, err := doRequest(client, http.MethodPost, origin+TelemetryPath, server.Address(), origin, []byte(`{"action":"reset_id"}`), bootstrap.CSRFToken)
	if err != nil || reset.StatusCode != http.StatusOK {
		t.Fatalf("reset = %d/%v", reset.StatusCode, err)
	}
	_ = reset.Body.Close()
	revoked, err := doRequest(client, http.MethodPost, origin+TelemetryPath, server.Address(), origin, []byte(`{"action":"revoke"}`), bootstrap.CSRFToken)
	if err != nil || revoked.StatusCode != http.StatusOK {
		t.Fatalf("revoke = %d/%v", revoked.StatusCode, err)
	}
	_ = revoked.Body.Close()
}

func TestTelemetryDisabledAdapterReportsUnavailable(t *testing.T) {
	service, err := telemetry.NewService(context.Background(), telemetry.ServiceOptions{
		Repository: &telemetryRepositoryStub{}, Prerequisites: telemetryPrerequisitesStub{}, Transport: telemetryTransportStub{},
		Clock: alertClockStub{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}, IDGenerator: &telemetryIDStub{values: []string{"00112233445566778899aabbccddeeff"}},
		AppVersion: "0.0.1-alpha", OSFamily: telemetry.OSLinux, Architecture: telemetry.ArchitectureAMD64,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(context.Background())
	response := telemetryResponse(mustTelemetryStatus(t, service))
	if response.Available || response.Enabled || response.Status != "unavailable" || response.InstallationIdPresent {
		t.Fatalf("disabled response = %#v", response)
	}
}

func TestBrowserTelemetryRejectsInvalidHostOriginSessionAndCSRF(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	repository := &telemetryRepositoryStub{}
	service, err := telemetry.NewService(context.Background(), telemetry.ServiceOptions{
		Repository:    repository,
		Prerequisites: telemetryPrerequisitesStub{value: telemetry.Prerequisites{Endpoint: true, PublicSchema: true, PrivacyNotice: true, EventRetention: true, AggregateRetention: true, Deletion: true, Reset: true}},
		Transport:     telemetryTransportStub{}, Clock: clock,
		IDGenerator: &telemetryIDStub{values: []string{"00112233445566778899aabbccddeeff"}},
		AppVersion:  "0.0.1-alpha", OSFamily: telemetry.OSLinux, Architecture: telemetry.ArchitectureAMD64,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	server, _, _ := startTestServer(t, Options{Telemetry: service, Clock: clock, SessionTTL: time.Minute})
	origin := server.Origin()
	bootstrapToken := mustBootstrapToken(t, server.BootstrapURL())

	missingSession, err := doRequest(testClient(t), http.MethodGet, origin+TelemetryPath, server.Address(), origin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	assertErrorResponse(t, missingSession, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid, bootstrapToken)

	client := testClient(t)
	exchange, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+bootstrapToken+`"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	var bootstrap BootstrapResponse
	if err := json.NewDecoder(exchange.Body).Decode(&bootstrap); err != nil {
		t.Fatal(err)
	}
	_ = exchange.Body.Close()

	hostileHost, err := doRequest(client, http.MethodGet, origin+TelemetryPath, "localhost:"+serverPort(server), origin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	assertErrorResponse(t, hostileHost, http.StatusBadRequest, apperrors.HTTPAPIHostInvalid, bootstrapToken)
	hostileOrigin, err := doRequest(client, http.MethodGet, origin+TelemetryPath, server.Address(), "http://evil.example", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	assertErrorResponse(t, hostileOrigin, http.StatusForbidden, apperrors.HTTPAPIOriginInvalid, bootstrapToken)

	for _, test := range []struct {
		name string
		body string
	}{
		{name: "consent", body: `{"action":"enable","schema_version":1}`},
		{name: "reset", body: `{"action":"reset_id"}`},
		{name: "revoke", body: `{"action":"revoke"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, csrf := range []string{"", "forged-csrf"} {
				response, requestErr := doRequest(client, http.MethodPost, origin+TelemetryPath, server.Address(), origin, []byte(test.body), csrf)
				if requestErr != nil {
					t.Fatal(requestErr)
				}
				secret := csrf
				if secret == "" {
					secret = bootstrapToken
				}
				assertErrorResponse(t, response, http.StatusForbidden, apperrors.HTTPAPICSRFInvalid, secret)
			}
		})
	}

	clock.now = clock.now.Add(2 * time.Minute)
	expired, err := doRequest(client, http.MethodGet, origin+TelemetryPath, server.Address(), origin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	assertErrorResponse(t, expired, http.StatusUnauthorized, apperrors.HTTPAPISessionExpired, bootstrapToken)
}

func mustTelemetryStatus(t *testing.T, service *telemetry.Service) telemetry.Snapshot {
	t.Helper()
	snapshot, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
