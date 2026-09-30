package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/vault"
)

func TestProductionDashboardUsesHTTPAndPrivateCommandsRemainPinned(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault, err := vault.NewMemoryVault(vault.MemoryVaultOptions{})
	if err != nil {
		t.Fatal(err)
	}
	opener := func(p platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
		return store.OpenWithOptions(store.Options{Path: p.DatabaseFile, Vault: secureVault})
	}
	start := func() (*productionCompanionFixture, *httpapi.CommandClient, string) {
		t.Helper()
		fixture := startProductionCompanionFixture(paths, serviceOptions{vaultMode: platform.VaultModePassphrase}, opener)
		t.Cleanup(fixture.Close)
		connection, err := waitForCompanionClient(paths, companionStartupTimeout, "", false)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(connection.Origin, "https://127.0.0.1:") || connection.CertificateSHA256 == "" {
			t.Fatal("private command descriptor lacks pinned TLS")
		}
		client, err := newServiceCommandClient(connection)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.UnlockVault(context.Background(), "synthetic-passphrase"); err != nil {
			t.Fatal(err)
		}
		connection, err = platform.DiscoverServiceClient(paths, platform.OwnerOptions{})
		if err != nil {
			t.Fatal(err)
		}
		client, err = newServiceCommandClient(connection)
		if err != nil {
			t.Fatal(err)
		}
		link, err := client.Dashboard(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Scheme != "http" || parsed.RawQuery != "" || parsed.Fragment == "" || parsed.Scheme+"://"+parsed.Host == connection.Origin {
			t.Fatal("dashboard was not separated from pinned command origin")
		}
		return fixture, client, link
	}
	fixture, client, link := start()
	secondLink, err := client.Dashboard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if secondLink == link {
		t.Fatal("dashboard launcher reused its bootstrap URL")
	}
	parsed, _ := url.Parse(link)
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(httpapi.BootstrapRequest{BootstrapToken: fragment.Get(httpapi.BootstrapQueryName)})
	origin := parsed.Scheme + "://" + parsed.Host
	request, _ := http.NewRequest(http.MethodPost, origin+httpapi.BootstrapPath, bytes.NewReader(body))
	request.Header.Set("Origin", origin)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("bootstrap status = %d", response.StatusCode)
	}
	if len(response.Header.Values("Set-Cookie")) != 0 {
		response.Body.Close()
		t.Fatal("HTTP bootstrap issued a cookie")
	}
	var session httpapi.BootstrapResponse
	err = json.NewDecoder(response.Body).Decode(&session)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if session.SessionToken == nil || *session.SessionToken == "" || session.CSRFToken == "" {
		t.Fatal("missing browser session material")
	}
	check := func(target, path string, want int) {
		t.Helper()
		request, _ := http.NewRequest(http.MethodGet, target+path, nil)
		request.Header.Set(httpapi.SessionHeaderName, *session.SessionToken)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("%s status = %d, want %d", path, response.StatusCode, want)
		}
	}
	check(origin, httpapi.MetadataPath, http.StatusOK)
	check(origin, httpapi.ProfilesPath, http.StatusOK)
	fixture.Close()
	_, _, restartedLink := start()
	restarted, _ := url.Parse(restartedLink)
	check(restarted.Scheme+"://"+restarted.Host, httpapi.MetadataPath, http.StatusUnauthorized)
}
