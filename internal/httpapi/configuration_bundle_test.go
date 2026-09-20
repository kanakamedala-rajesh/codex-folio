package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configbundle"
)

type configurationBundleRepositoryStub struct{}

func (configurationBundleRepositoryStub) SetConfigurationAppearance(context.Context, string) error {
	return nil
}

func (configurationBundleRepositoryStub) ExportConfiguration(context.Context, bool) (configbundle.Bundle, error) {
	return configbundle.Bundle{SchemaVersion: 1, Profiles: []configbundle.Profile{}, ConfigurationPacks: []configbundle.Pack{}, AlertThresholds: []configbundle.Threshold{}, OperationalPreferences: configbundle.Preferences{CollectionActiveSeconds: 300, CollectionIdleSeconds: 1800, Appearance: "system"}}, nil
}
func (configurationBundleRepositoryStub) PreviewConfigurationImport(context.Context, configbundle.Bundle) (configbundle.Preview, error) {
	return configbundle.Preview{}, nil
}
func (configurationBundleRepositoryStub) ApplyConfigurationImport(context.Context, configbundle.Bundle, configbundle.ApplyRequest) (configbundle.ApplyResult, error) {
	return configbundle.ApplyResult{}, nil
}

func TestBrowserConfigurationBundleUsesSessionOriginHostAndCSRFBoundaries(t *testing.T) {
	service, err := configbundle.NewService(configurationBundleRepositoryStub{})
	if err != nil {
		t.Fatal(err)
	}
	server, _, _ := startTestServer(t, Options{ConfigurationBundles: service})
	origin := server.Origin()
	bootstrapToken := mustBootstrapToken(t, server.BootstrapURL())
	missing, err := doRequest(testClient(t), http.MethodGet, origin+ConfigurationBundlePath, server.Address(), origin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	assertErrorResponse(t, missing, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid, bootstrapToken)
	client := testClient(t)
	exchange, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+bootstrapToken+`"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	var bootstrap BootstrapResponse
	if err = json.NewDecoder(exchange.Body).Decode(&bootstrap); err != nil {
		t.Fatal(err)
	}
	exchange.Body.Close()
	hostileHost, err := doRequest(client, http.MethodGet, origin+ConfigurationBundlePath, "localhost:"+serverPort(server), origin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	assertErrorResponse(t, hostileHost, http.StatusBadRequest, apperrors.HTTPAPIHostInvalid, bootstrapToken)
	hostileOrigin, err := doRequest(client, http.MethodGet, origin+ConfigurationBundlePath, server.Address(), "http://evil.example", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	assertErrorResponse(t, hostileOrigin, http.StatusForbidden, apperrors.HTTPAPIOriginInvalid, bootstrapToken)
	for _, csrf := range []string{"", "forged"} {
		response, requestErr := doRequest(client, http.MethodPost, origin+ConfigurationBundlePath, server.Address(), origin, []byte(`{"action":"import_preview","bundle":{}}`), csrf)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		assertErrorResponse(t, response, http.StatusForbidden, apperrors.HTTPAPICSRFInvalid, bootstrapToken)
	}
	ok, err := doRequest(client, http.MethodGet, origin+ConfigurationBundlePath, server.Address(), origin, nil, "")
	if err != nil || ok.StatusCode != http.StatusOK {
		t.Fatalf("export preview=%d/%v", ok.StatusCode, err)
	}
	var response ConfigurationBundleResponse
	if err = json.NewDecoder(ok.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	ok.Body.Close()
	if response.Preview == nil || response.Preview.Bundle == nil || response.Preview.Bundle.SchemaVersion != 1 {
		t.Fatalf("response=%#v", response)
	}
}

func TestCommandConfigurationBundleRequiresCommandToken(t *testing.T) {
	service, _ := configbundle.NewService(configurationBundleRepositoryStub{})
	const token = "configuration-command-token"
	server, _, _ := startTestServer(t, Options{ConfigurationBundles: service, CommandToken: token})
	origin := server.Origin()
	response, err := doRequest(testClient(t), http.MethodGet, origin+CommandConfigurationBundlePath, server.Address(), origin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	assertErrorResponse(t, response, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid, token)
	request, err := http.NewRequest(http.MethodGet, origin+CommandConfigurationBundlePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = server.Address()
	request.Header.Set("Origin", origin)
	request.Header.Set(CommandTokenHeader, token)
	response, err = testClient(t).Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("command=%d/%v", response.StatusCode, err)
	}
	response.Body.Close()
}
