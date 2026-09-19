package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
)

type diagnosticsRepositoryStub struct {
	settings   diagnostics.Settings
	aggregates []diagnostics.Aggregate
}

func (repository *diagnosticsRepositoryStub) DiagnosticSettings(context.Context) (diagnostics.Settings, error) {
	return repository.settings, nil
}

func (repository *diagnosticsRepositoryStub) SetDiagnosticSettings(_ context.Context, settings diagnostics.Settings) (diagnostics.Settings, error) {
	repository.settings = settings
	return settings, nil
}

func (repository *diagnosticsRepositoryStub) ListDiagnosticAggregates(context.Context) ([]diagnostics.Aggregate, error) {
	return append([]diagnostics.Aggregate(nil), repository.aggregates...), nil
}

func TestBrowserDiagnosticsRequireSessionAndCSRFAndExportOnlyReviewedBundle(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	event, err := diagnostics.NewEvent(now, diagnostics.SeverityError, diagnostics.ComponentStore, apperrors.StoreReadFailed, diagnostics.Context{})
	if err != nil {
		t.Fatal(err)
	}
	repository := &diagnosticsRepositoryStub{
		settings: diagnostics.DefaultSettings(),
		aggregates: []diagnostics.Aggregate{{
			ID: diagnostics.AggregateID(event), Component: diagnostics.ComponentStore, ErrorCode: apperrors.StoreReadFailed,
			Severity: diagnostics.SeverityError, OccurrenceCount: 2, FirstSeenAt: now, LastSeenAt: now,
		}},
	}
	service, err := diagnostics.NewService(diagnostics.ServiceOptions{
		Repository: repository,
		Clock:      alertClockStub{now: now},
		Environment: func(context.Context) (diagnostics.BundleEnvironment, error) {
			return diagnostics.BundleEnvironment{
				ApplicationVersion: "0.0.1-alpha", DatabaseSchemaVersion: 22,
				OSFamily: diagnostics.OSLinux, Architecture: diagnostics.ArchitectureAMD64,
				Features: diagnostics.FeatureStates{Service: true},
				Health:   diagnostics.Health{Service: diagnostics.HealthHealthy, Database: diagnostics.HealthHealthy, Vault: diagnostics.HealthHealthy},
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, _, _ := startTestServer(t, Options{DiagnosticService: service})
	client := testClient(t)
	origin := server.Origin()
	unauthorized, err := doRequest(client, http.MethodGet, origin+DiagnosticsPath, server.Address(), origin, nil, "")
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

	withoutCSRF, err := doRequest(client, http.MethodPost, origin+DiagnosticsPath, server.Address(), origin, []byte(`{"action":"preview"}`), "")
	if err != nil || withoutCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("preview without CSRF = %d/%v", withoutCSRF.StatusCode, err)
	}
	_ = withoutCSRF.Body.Close()
	configured, err := doRequest(client, http.MethodPost, origin+DiagnosticsPath, server.Address(), origin, []byte(`{"action":"configure","settings":{"enabled":false,"minimum_level":"warning","retention_days":7}}`), bootstrap.CSRFToken)
	if err != nil || configured.StatusCode != http.StatusOK {
		t.Fatalf("configure = %d/%v", configured.StatusCode, err)
	}
	_ = configured.Body.Close()
	if repository.settings.Enabled || repository.settings.MinimumLevel != diagnostics.LevelWarning || repository.settings.RetentionDays != 7 {
		t.Fatalf("settings = %#v", repository.settings)
	}

	previewResponse, err := doRequest(client, http.MethodPost, origin+DiagnosticsPath, server.Address(), origin, []byte(`{"action":"preview"}`), bootstrap.CSRFToken)
	if err != nil || previewResponse.StatusCode != http.StatusOK {
		t.Fatalf("preview = %d/%v", previewResponse.StatusCode, err)
	}
	var preview DiagnosticsResponse
	if err := json.NewDecoder(previewResponse.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	_ = previewResponse.Body.Close()
	if preview.Preview == nil || preview.Preview.DiagnosticCount != 1 || preview.Preview.Bundle.Settings.Enabled {
		t.Fatalf("preview = %#v", preview.Preview)
	}
	stale := "wrong"
	rejected, err := doRequest(client, http.MethodPost, origin+DiagnosticsPath, server.Address(), origin, []byte(`{"action":"export","confirmation":"`+stale+`"}`), bootstrap.CSRFToken)
	if err != nil || rejected.StatusCode != http.StatusConflict {
		t.Fatalf("stale export = %d/%v", rejected.StatusCode, err)
	}
	assertErrorResponse(t, rejected, http.StatusConflict, apperrors.DiagnosticsConfirmationInvalid, diagnostics.AggregateID(event))
	confirmed, err := doRequest(client, http.MethodPost, origin+DiagnosticsPath, server.Address(), origin, []byte(`{"action":"export","confirmation":"`+preview.Preview.ConfirmationDigest+`"}`), bootstrap.CSRFToken)
	if err != nil || confirmed.StatusCode != http.StatusOK {
		t.Fatalf("confirmed export = %d/%v", confirmed.StatusCode, err)
	}
	var exported DiagnosticsResponse
	if err := json.NewDecoder(confirmed.Body).Decode(&exported); err != nil {
		t.Fatal(err)
	}
	_ = confirmed.Body.Close()
	if exported.Bundle == nil || exported.Bundle.GeneratedAt != preview.Preview.Bundle.GeneratedAt || len(exported.Bundle.Diagnostics) != 1 {
		t.Fatalf("export = %#v", exported.Bundle)
	}
}
