package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/updates"
)

type updateRepositoryStub struct {
	settings updates.Settings
	state    updates.CheckState
}

func (repository *updateRepositoryStub) UpdateSettings(context.Context) (updates.Settings, error) {
	return repository.settings, nil
}
func (repository *updateRepositoryStub) SetUpdateSettings(_ context.Context, value updates.Settings) (updates.Settings, error) {
	repository.settings = value
	return value, nil
}
func (repository *updateRepositoryStub) UpdateCheckState(context.Context) (updates.CheckState, error) {
	if repository.state.Status == "" {
		return updates.CheckState{Status: updates.StatusNeverChecked}, nil
	}
	return repository.state, nil
}
func (repository *updateRepositoryStub) SetUpdateCheckState(_ context.Context, value updates.CheckState) (updates.CheckState, error) {
	repository.state = value
	return value, nil
}

type updateSourceStub struct{ calls int }

func (*updateSourceStub) Configured() bool { return true }
func (source *updateSourceStub) Check(context.Context) (updates.Evidence, error) {
	source.calls++
	return updates.Evidence{
		Version: "0.0.2-alpha", ReleaseNotes: "Validated release notes.",
		DownloadURL:       "https://downloads.example.test/codex-folio/0.0.2-alpha/",
		InstallerGuidance: "Download and run the platform installer.",
	}, nil
}

func TestBrowserUpdatesRequireConsentBoundaryAndCSRF(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	repository := &updateRepositoryStub{}
	source := &updateSourceStub{}
	service, err := updates.NewService(updates.ServiceOptions{Repository: repository, Source: source, Clock: alertClockStub{now: now}, CurrentVersion: "0.0.1-alpha"})
	if err != nil {
		t.Fatal(err)
	}
	server, _, _ := startTestServer(t, Options{Updates: service})
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

	status, err := doRequest(client, http.MethodGet, origin+UpdatesPath, server.Address(), origin, nil, "")
	if err != nil || status.StatusCode != http.StatusOK {
		t.Fatalf("status = %d/%v", status.StatusCode, err)
	}
	var initial UpdateResponse
	if err := json.NewDecoder(status.Body).Decode(&initial); err != nil {
		t.Fatal(err)
	}
	_ = status.Body.Close()
	if source.calls != 0 || initial.Status != string(updates.StatusDisabled) || initial.AutomaticChecks {
		t.Fatalf("initial = %#v; source calls = %d", initial, source.calls)
	}

	withoutCSRF, err := doRequest(client, http.MethodPost, origin+UpdatesPath, server.Address(), origin, []byte(`{"action":"check"}`), "")
	if err != nil || withoutCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("check without CSRF = %d/%v", withoutCSRF.StatusCode, err)
	}
	_ = withoutCSRF.Body.Close()

	checked, err := doRequest(client, http.MethodPost, origin+UpdatesPath, server.Address(), origin, []byte(`{"action":"check"}`), bootstrap.CSRFToken)
	if err != nil || checked.StatusCode != http.StatusOK {
		t.Fatalf("explicit check = %d/%v", checked.StatusCode, err)
	}
	var result UpdateResponse
	if err := json.NewDecoder(checked.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	_ = checked.Body.Close()
	if source.calls != 1 || result.Status != string(updates.StatusUpdateAvailable) || result.AutomaticChecks {
		t.Fatalf("check = %#v; source calls = %d", result, source.calls)
	}
	if result.DownloadUrl == "" || result.ReleaseNotes == "" || result.InstallerGuidance == "" {
		t.Fatalf("validated evidence missing: %#v", result)
	}

	enabled, err := doRequest(client, http.MethodPost, origin+UpdatesPath, server.Address(), origin, []byte(`{"action":"configure","automatic_checks":true}`), bootstrap.CSRFToken)
	if err != nil || enabled.StatusCode != http.StatusOK {
		t.Fatalf("enable = %d/%v", enabled.StatusCode, err)
	}
	_ = enabled.Body.Close()
	if source.calls != 1 || !repository.settings.AutomaticChecks {
		t.Fatalf("configuration performed a check: calls=%d settings=%#v", source.calls, repository.settings)
	}
}
