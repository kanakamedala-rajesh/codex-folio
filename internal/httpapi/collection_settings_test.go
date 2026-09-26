package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/usage"
)

func TestBrowserCollectionSettingsRequireCSRFAndPreserveExplicitConsent(t *testing.T) {
	service := &collectionSettingsStub{settings: usage.DefaultCollectionSettings(), consent: usage.CollectionConsentUndecided}
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
	if current.ActiveIntervalSeconds != 300 || current.IdleIntervalSeconds != 1800 || current.SchedulerEnabled || current.Consent != "undecided" || current.ProviderFloorBasis != providerFloorBasis {
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
	accepted, err := doRequest(client, http.MethodPut, origin+CollectionSettingsPath, server.Address(), origin, []byte(`{"active_interval_seconds":600,"idle_interval_seconds":2700,"consent":"accepted"}`), bootstrap.CSRFToken)
	if err != nil || accepted.StatusCode != http.StatusOK {
		t.Fatalf("consent PUT status/error = %d/%v", accepted.StatusCode, err)
	}
	var acceptedSettings CollectionSettingsResponse
	if err := json.NewDecoder(accepted.Body).Decode(&acceptedSettings); err != nil {
		t.Fatal(err)
	}
	_ = accepted.Body.Close()
	if !acceptedSettings.SchedulerEnabled || acceptedSettings.Consent != "accepted" {
		t.Fatalf("accepted settings = %#v", acceptedSettings)
	}
	invalid, err := doRequest(client, http.MethodPut, origin+CollectionSettingsPath, server.Address(), origin, []byte(`{"active_interval_seconds":600,"idle_interval_seconds":2700,"consent":"undecided"}`), bootstrap.CSRFToken)
	if err != nil || invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid consent status/error = %d/%v", invalid.StatusCode, err)
	}
	_ = invalid.Body.Close()
	if service.consent != usage.CollectionConsentAccepted {
		t.Fatalf("invalid request changed consent: %s", service.consent)
	}
}

type collectionSettingsStub struct {
	settings usage.CollectionSettings
	consent  usage.CollectionConsent
	setCalls int
}

func (service *collectionSettingsStub) CollectionSettings(context.Context) (usage.CollectionSettings, usage.CollectionConsent, error) {
	return service.settings, service.consent, nil
}

func (service *collectionSettingsStub) SetCollectionSettings(_ context.Context, settings usage.CollectionSettings, consent *usage.CollectionConsent) (usage.CollectionSettings, usage.CollectionConsent, error) {
	service.setCalls++
	if err := usage.ValidateCollectionSettings(settings); err != nil {
		return usage.CollectionSettings{}, service.consent, err
	}
	service.settings = settings
	if consent != nil {
		service.consent = *consent
	}
	return settings, service.consent, nil
}
