package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"venkatasudha.com/codex-folio/internal/buildinfo"
)

func TestGeneratedClientRoundTripsMetadataFixture(t *testing.T) {
	t.Parallel()

	fixture, err := os.ReadFile(filepath.Join("..", "..", "api", "fixtures", "metadata-response.json"))
	if err != nil {
		t.Fatalf("read metadata fixture: %v", err)
	}
	want := MetadataResponse{
		APIVersion:      APIVersion,
		ContractVersion: buildinfo.Version,
		Product:         buildinfo.ProductName,
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal generated metadata response: %v", err)
	}
	assertJSONEqual(t, encoded, fixture)

	httpClient := &recordingHTTPDoer{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(fixture)),
		},
	}
	got, response, err := NewClient("http://127.0.0.1", httpClient).GetMetadata(context.Background())
	if err != nil {
		t.Fatalf("GetMetadata() error: %v", err)
	}
	if httpClient.request == nil {
		t.Fatal("GetMetadata() did not send a request")
	}
	if httpClient.request.Method != http.MethodGet {
		t.Fatalf("request method = %q, want %q", httpClient.request.Method, http.MethodGet)
	}
	if httpClient.request.URL.Path != MetadataPath {
		t.Fatalf("request path = %q, want %q", httpClient.request.URL.Path, MetadataPath)
	}
	if httpClient.request.Header.Get("Accept") != "application/json" {
		t.Fatalf("Accept header = %q, want application/json", httpClient.request.Header.Get("Accept"))
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GetMetadata() status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if got != want {
		t.Fatalf("GetMetadata() = %#v, want %#v", got, want)
	}
}

func TestGeneratedClientBuildsBootstrapExchangeRequest(t *testing.T) {
	t.Parallel()

	httpClient := &recordingHTTPDoer{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"csrf_token":"csrf-value"}`))),
		},
	}
	got, response, err := NewClient("http://127.0.0.1", httpClient).ExchangeBootstrap(
		context.Background(),
		BootstrapRequest{BootstrapToken: "bootstrap-value"},
	)
	if err != nil {
		t.Fatalf("ExchangeBootstrap() error: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("ExchangeBootstrap() status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if got.CSRFToken != "csrf-value" {
		t.Fatalf("ExchangeBootstrap() = %#v, want CSRF response", got)
	}
	if httpClient.request == nil {
		t.Fatal("ExchangeBootstrap() did not send a request")
	}
	if httpClient.request.Method != http.MethodPost || httpClient.request.URL.Path != BootstrapPath {
		t.Fatalf("request = %s %s, want POST %s", httpClient.request.Method, httpClient.request.URL.Path, BootstrapPath)
	}
	if httpClient.request.Header.Get("Accept") != "application/json" || httpClient.request.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("request headers = %#v, want JSON Accept and Content-Type", httpClient.request.Header)
	}
	body, err := io.ReadAll(httpClient.request.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var input BootstrapRequest
	if err := json.Unmarshal(body, &input); err != nil {
		t.Fatalf("bootstrap request body is not JSON: %v", err)
	}
	if input.BootstrapToken != "bootstrap-value" {
		t.Fatalf("bootstrap request token = %q, want bootstrap-value", input.BootstrapToken)
	}
}

func TestGeneratedClientBuildsUsageRefreshRequest(t *testing.T) {
	t.Parallel()
	fixture := []byte(`{"snapshot_id":"snapshot-1","profile_id":"profile-1","alias":"Work","source":"codex_app_server","source_version":"0.153.4","captured_at":"2026-09-06T12:00:00Z","observations":[],"availability":[]}`)
	httpClient := &recordingHTTPDoer{response: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(fixture))}}
	triggerReason := "dashboard_refresh"
	got, response, err := NewClient("http://127.0.0.1", httpClient).RefreshUsage(context.Background(), UsageRefreshRequest{Alias: "Work", TriggerReason: &triggerReason})
	if err != nil {
		t.Fatalf("RefreshUsage() error: %v", err)
	}
	if response.StatusCode != http.StatusOK || got.SnapshotId != "snapshot-1" {
		t.Fatalf("RefreshUsage() = %#v, status %d", got, response.StatusCode)
	}
	if httpClient.request.Method != http.MethodPost || httpClient.request.URL.Path != UsageRefreshPath {
		t.Fatalf("request = %s %s", httpClient.request.Method, httpClient.request.URL.Path)
	}
	body, err := io.ReadAll(httpClient.request.Body)
	if err != nil || string(body) != `{"alias":"Work","trigger_reason":"dashboard_refresh"}` {
		t.Fatalf("request body = %q/%v", body, err)
	}
}

func TestGeneratedClientBuildsLatestUsageRequest(t *testing.T) {
	t.Parallel()
	fixture := []byte(`{"snapshot_id":"snapshot-1","profile_id":"profile-1","alias":"Work","source":"codex_app_server","source_version":"0.153.4","captured_at":"2026-09-06T12:00:00Z","status":"partial","observations":[],"availability":[]}`)
	httpClient := &recordingHTTPDoer{response: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(fixture))}}
	got, response, err := NewClient("http://127.0.0.1", httpClient).GetLatestUsage(context.Background(), "Work")
	if err != nil || response.StatusCode != http.StatusOK || got.SnapshotId != "snapshot-1" {
		t.Fatalf("GetLatestUsage() = %#v/%v", got, err)
	}
	if httpClient.request.Method != http.MethodGet || httpClient.request.URL.Path != UsageLatestPath || httpClient.request.URL.Query().Get("alias") != "Work" {
		t.Fatalf("request = %s %s", httpClient.request.Method, httpClient.request.URL.String())
	}
}

func TestGeneratedUsageClientExposesSafeError(t *testing.T) {
	t.Parallel()
	httpClient := &recordingHTTPDoer{response: &http.Response{
		StatusCode: http.StatusConflict,
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"code":"CF_USAGE_PROFILE_UNAVAILABLE","message":"Usage is unavailable for this profile."}`))),
	}}
	_, _, err := NewClient("http://127.0.0.1", httpClient).RefreshUsage(context.Background(), UsageRefreshRequest{Alias: "Work"})
	var failure UsageErrorResponse
	if !errors.As(err, &failure) || failure.Code != "CF_USAGE_PROFILE_UNAVAILABLE" || failure.Message != "Usage is unavailable for this profile." {
		t.Fatalf("RefreshUsage() error = %#v", err)
	}
}

func TestGeneratedUsageErrorExcludesSnapshotEvidence(t *testing.T) {
	t.Parallel()
	payload, err := json.Marshal(UsageErrorResponse{Code: "CF_USAGE_COLLECTION_FAILED", Message: "Usage refresh failed."})
	if err != nil {
		t.Fatal(err)
	}
	for _, prohibited := range []string{"snapshot", "profile", "alias", "value", "observations"} {
		if bytes.Contains(payload, []byte(prohibited)) {
			t.Fatalf("error payload %s contains %q", payload, prohibited)
		}
	}
}

type recordingHTTPDoer struct {
	request  *http.Request
	response *http.Response
}

func (client *recordingHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	client.request = request
	return client.response, nil
}

func assertJSONEqual(t *testing.T, left, right []byte) {
	t.Helper()

	var leftValue, rightValue map[string]string
	if err := json.Unmarshal(left, &leftValue); err != nil {
		t.Fatalf("decode generated JSON: %v", err)
	}
	if err := json.Unmarshal(right, &rightValue); err != nil {
		t.Fatalf("decode fixture JSON: %v", err)
	}
	if len(leftValue) != len(rightValue) {
		t.Fatalf("JSON field count = %d, want %d", len(leftValue), len(rightValue))
	}
	for key, want := range rightValue {
		if leftValue[key] != want {
			t.Errorf("JSON field %q = %q, want %q", key, leftValue[key], want)
		}
	}
}
