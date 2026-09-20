package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	updatesadapter "venkatasudha.com/codex-folio/internal/adapters/updates"
	"venkatasudha.com/codex-folio/internal/buildinfo"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/updates"
)

func TestUpdatesCLIUsesRunningAuthenticatedServiceAndIsolatesFixtureFailures(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.OpenWithVault(paths.DatabaseFile, secureVault)
	if err != nil {
		t.Fatal(err)
	}

	var mode atomic.Value
	mode.Store("up_to_date")
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch mode.Load().(string) {
		case "offline":
			panic(http.ErrAbortHandler)
		case "malformed":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{malformed`)
			return
		case "unavailable":
			http.Error(response, "fixture unavailable", http.StatusServiceUnavailable)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(response, `{"schema_version":1,"version":%q,"release_notes":"Fixture notes.","download_url":%q,"installer_guidance":"Use the platform installer."}`, buildinfo.Version, "https://"+request.Host+"/downloads/"+buildinfo.Version+"/")
	}))
	updateSource, err := updatesadapter.NewHTTPSSource(updatesadapter.HTTPSOptions{
		ManifestURL: fixture.URL + "/manifest.json", AllowedDownloadPrefix: fixture.URL + "/downloads/", Client: fixture.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	updateService, err := updates.NewService(updates.ServiceOptions{Repository: state, Source: updateSource, Clock: usageClock{}, CurrentVersion: buildinfo.Version})
	if err != nil {
		t.Fatal(err)
	}
	const token = "updates-running-owner-token"
	server, err := httpapi.NewServer(httpapi.Options{Updates: updateService, CommandToken: token})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: token}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		fixture.Close()
		_ = state.Close()
		_ = owner.Close()
	})

	for _, test := range []struct{ mode, status string }{
		{"up_to_date", "up_to_date"},
		{"malformed", "malformed"},
		{"offline", "offline"},
		{"unavailable", "unavailable"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			mode.Store(test.mode)
			var stdout, stderr bytes.Buffer
			code := runUpdates([]string{"check", "--json"}, &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil })
			if code != exitSuccess || stderr.Len() != 0 {
				t.Fatalf("runUpdates() = %d, stderr=%q", code, stderr.String())
			}
			var response httpapi.UpdateResponse
			if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
				t.Fatalf("response %q: %v", stdout.String(), err)
			}
			if response.Status != test.status || response.AutomaticChecks {
				t.Fatalf("response = %#v, want status %q and consent off", response, test.status)
			}
		})
	}

	var stdout, stderr bytes.Buffer
	code := runUpdates([]string{"status", "--json"}, &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil })
	if code != exitSuccess || stderr.Len() != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"status":"unavailable"`)) {
		t.Fatalf("local status after failure = %d/%q/%q", code, stdout.String(), stderr.String())
	}
}

func TestParseUpdateOptionsKeepsExplicitCheckSeparateFromAutomaticConsent(t *testing.T) {
	check, err := parseUpdateOptions([]string{"check", "--json"})
	if err != nil || check.action != "check" || check.automatic != nil || !check.json {
		t.Fatalf("check options = %#v, %v", check, err)
	}
	enabled, err := parseUpdateOptions([]string{"settings", "--automatic", "true"})
	if err != nil || enabled.automatic == nil || !*enabled.automatic {
		t.Fatalf("settings options = %#v, %v", enabled, err)
	}
	if _, err := parseUpdateOptions([]string{"check", "--automatic", "true"}); err == nil {
		t.Fatal("explicit check accepted automatic consent")
	}
}

func TestWriteUpdateStatusShowsDownloadEvidenceOnlyForAvailableUpdate(t *testing.T) {
	var unavailable bytes.Buffer
	writeUpdateStatus(&unavailable, httpapi.UpdateResponse{Status: "malformed", CurrentVersion: "0.0.1-alpha", DownloadUrl: "https://malicious.example/"})
	if bytes.Contains(unavailable.Bytes(), []byte("malicious")) {
		t.Fatalf("malformed response exposed download location: %q", unavailable.String())
	}

	var available bytes.Buffer
	writeUpdateStatus(&available, httpapi.UpdateResponse{
		Status: "update_available", CurrentVersion: "0.0.1-alpha", AvailableVersion: "0.0.2-alpha",
		ReleaseNotes: "Safe notes", DownloadUrl: "https://downloads.example.test/codex-folio/0.0.2-alpha/", InstallerGuidance: "Use the installer.",
	})
	for _, want := range []string{"0.0.2-alpha", "Safe notes", "downloads.example.test", "Use the installer"} {
		if !bytes.Contains(available.Bytes(), []byte(want)) {
			t.Errorf("output %q does not contain %q", available.String(), want)
		}
	}
}
