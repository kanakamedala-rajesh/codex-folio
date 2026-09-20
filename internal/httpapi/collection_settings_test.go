package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/usage"
)

func TestBrowserCollectionSettingsRequireCSRFAndPreserveExplicitEnrollmentState(t *testing.T) {
	service := &collectionSettingsStub{settings: usage.DefaultCollectionSettings(), enabled: true}
	server, _, _ := startTestServer(t, Options{CollectionSettings: service})
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

	read, err := doRequest(client, http.MethodGet, origin+CollectionSettingsPath, server.Address(), origin, nil, "")
	if err != nil || read.StatusCode != http.StatusOK {
		t.Fatalf("GET status/error = %d/%v", read.StatusCode, err)
	}
	var current CollectionSettingsResponse
	if err := json.NewDecoder(read.Body).Decode(&current); err != nil {
		t.Fatal(err)
	}
	_ = read.Body.Close()
	if current.ActiveIntervalSeconds != 300 || current.IdleIntervalSeconds != 1800 || !current.SchedulerEnabled || current.ProviderFloorBasis != providerFloorBasis {
		t.Fatalf("GET result = %#v", current)
	}

	withoutCSRF, err := doRequest(client, http.MethodPut, origin+CollectionSettingsPath, server.Address(), origin, []byte(`{"active_interval_seconds":600,"idle_interval_seconds":2700}`), "")
	if err != nil || withoutCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("PUT without CSRF status/error = %d/%v", withoutCSRF.StatusCode, err)
	}
	_ = withoutCSRF.Body.Close()
	overflow, err := doRequest(client, http.MethodPut, origin+CollectionSettingsPath, server.Address(), origin, []byte(`{"active_interval_seconds":36028797018964268,"idle_interval_seconds":1800}`), bootstrap.CSRFToken)
	if err != nil || overflow.StatusCode != http.StatusBadRequest {
		t.Fatalf("overflowing PUT status/error = %d/%v", overflow.StatusCode, err)
	}
	_ = overflow.Body.Close()
	if service.setCalls != 0 || service.settings != usage.DefaultCollectionSettings() {
		t.Fatalf("overflowing PUT persisted settings: calls=%d settings=%#v", service.setCalls, service.settings)
	}
	updated, err := doRequest(client, http.MethodPut, origin+CollectionSettingsPath, server.Address(), origin, []byte(`{"active_interval_seconds":600,"idle_interval_seconds":2700}`), bootstrap.CSRFToken)
	if err != nil || updated.StatusCode != http.StatusOK {
		t.Fatalf("PUT status/error = %d/%v body=%s", updated.StatusCode, err, readBody(t, updated))
	}
	_ = updated.Body.Close()
	if service.settings.ActiveInterval != 10*time.Minute || service.settings.IdleInterval != 45*time.Minute {
		t.Fatalf("persisted settings = %#v", service.settings)
	}
}

type collectionSettingsStub struct {
	settings usage.CollectionSettings
	enabled  bool
	setCalls int
}

func (service *collectionSettingsStub) CollectionSettings(context.Context) (usage.CollectionSettings, bool, error) {
	return service.settings, service.enabled, nil
}

func (service *collectionSettingsStub) SetCollectionSettings(_ context.Context, settings usage.CollectionSettings) (usage.CollectionSettings, bool, error) {
	service.setCalls++
	if err := usage.ValidateCollectionSettings(settings); err != nil {
		return usage.CollectionSettings{}, service.enabled, err
	}
	service.settings = settings
	return settings, service.enabled, nil
}
