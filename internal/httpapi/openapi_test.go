package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
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
