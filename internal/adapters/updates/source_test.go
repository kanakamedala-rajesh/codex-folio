package updatesadapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/updates"
)

func TestHTTPSSourceReadsStrictBoundedManifestWithoutFetchingArtifact(t *testing.T) {
	var artifactCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/releases/1.2.3.zip" {
			artifactCalls.Add(1)
			http.Error(writer, "must not fetch", http.StatusInternalServerError)
			return
		}
		if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" || request.Method != http.MethodGet {
			t.Errorf("unsafe request metadata: method=%s authorization=%q cookie=%q", request.Method, request.Header.Get("Authorization"), request.Header.Get("Cookie"))
		}
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = fmt.Fprintf(writer, `{"schema_version":1,"version":"1.2.3","release_notes":"Safe notes","download_url":%q,"installer_guidance":"Stop the service and verify the signature."}`, serverURL(request)+"/releases/1.2.3.zip")
	}))
	defer server.Close()

	source, err := NewHTTPSSource(HTTPSOptions{ManifestURL: server.URL + "/manifest.json", AllowedDownloadPrefix: server.URL + "/releases/", Client: server.Client()})
	if err != nil {
		t.Fatalf("NewHTTPSSource() error = %v", err)
	}
	evidence, err := source.Check(context.Background())
	if err != nil || evidence.Version != "1.2.3" || artifactCalls.Load() != 0 {
		t.Fatalf("Check() = %#v, %v; artifact calls=%d", evidence, err, artifactCalls.Load())
	}
}

func TestHTTPSSourceRejectsMaliciousAndMalformedResponses(t *testing.T) {
	tests := []struct{ name, contentType, body string }{
		{"malicious download", "application/json", `{"schema_version":1,"version":"1.2.3","release_notes":"notes","download_url":"https://evil.example/release.zip","installer_guidance":"guidance"}`},
		{"traversing download", "application/json", `{"schema_version":1,"version":"1.2.3","release_notes":"notes","download_url":"https://updates.example/releases/../evil.zip","installer_guidance":"guidance"}`},
		{"unknown field", "application/json", `{"schema_version":1,"version":"1.2.3","release_notes":"notes","download_url":"https://updates.example/releases/a.zip","installer_guidance":"guidance","extra":true}`},
		{"trailing document", "application/json", `{"schema_version":1,"version":"1.2.3","release_notes":"notes","download_url":"https://updates.example/releases/a.zip","installer_guidance":"guidance"} {}`},
		{"wrong content type", "text/plain", `{}`},
		{"oversize", "application/json", `{"schema_version":1,"version":"1.2.3","release_notes":"notes","download_url":"https://updates.example/releases/a.zip","installer_guidance":"guidance"}` + strings.Repeat(" ", maximumManifestBytes)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", test.contentType)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			base := server.URL + "/releases/"
			if strings.Contains(test.body, "updates.example") {
				base = "https://updates.example/releases/"
			}
			source, err := NewHTTPSSource(HTTPSOptions{ManifestURL: server.URL + "/manifest", AllowedDownloadPrefix: base, Client: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			_, err = source.Check(context.Background())
			if !errors.Is(err, updates.ErrSourceMalformed) {
				t.Fatalf("Check() error = %v", err)
			}
		})
	}
}

func TestHTTPSSourceRejectsRedirectBeforeFollowing(t *testing.T) {
	var destinationCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/destination" {
			destinationCalls.Add(1)
			return
		}
		http.Redirect(writer, request, "/destination", http.StatusFound)
	}))
	defer server.Close()
	source, err := NewHTTPSSource(HTTPSOptions{ManifestURL: server.URL + "/manifest", AllowedDownloadPrefix: server.URL + "/releases/", Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Check(context.Background())
	if !errors.Is(err, updates.ErrSourceUnavailable) || destinationCalls.Load() != 0 {
		t.Fatalf("Check() = %v; destination calls=%d", err, destinationCalls.Load())
	}
}

func TestHTTPSSourceClassifiesOfflineAndRespectsCancellation(t *testing.T) {
	offline := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("private DNS detail") })}
	source, err := NewHTTPSSource(HTTPSOptions{ManifestURL: "https://updates.example/manifest", AllowedDownloadPrefix: "https://updates.example/releases/", Client: offline})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Check(context.Background()); !errors.Is(err, updates.ErrSourceOffline) || strings.Contains(err.Error(), "DNS") {
		t.Fatalf("offline error = %v", err)
	}

	cancelled := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	source, err = NewHTTPSSource(HTTPSOptions{ManifestURL: "https://updates.example/manifest", AllowedDownloadPrefix: "https://updates.example/releases/", Client: cancelled})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.Check(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestHTTPSSourceBoundsStalledHeadersAndBodies(t *testing.T) {
	for _, test := range []struct {
		name      string
		transport http.RoundTripper
	}{
		{
			name: "headers",
			transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				<-request.Context().Done()
				return nil, request.Context().Err()
			}),
		},
		{
			name: "body",
			transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       contextBody{ctx: request.Context()},
					Request:    request,
				}, nil
			}),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, err := NewHTTPSSource(HTTPSOptions{
				ManifestURL: "https://updates.example/manifest", AllowedDownloadPrefix: "https://updates.example/releases/",
				Client: &http.Client{Transport: test.transport}, AttemptTimeout: time.Millisecond,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.Check(context.Background()); !errors.Is(err, updates.ErrSourceUnavailable) {
				t.Fatalf("Check() error = %v", err)
			}
		})
	}
}

func TestDisabledSourceNeverConfiguresNetwork(t *testing.T) {
	source := DisabledSource()
	if source.Configured() {
		t.Fatal("DisabledSource configured")
	}
	if _, err := source.Check(context.Background()); !errors.Is(err, updates.ErrSourceUnconfigured) {
		t.Fatalf("Check() = %v", err)
	}
}

func TestHTTPSSourceRejectsUntrustedConfiguration(t *testing.T) {
	for _, options := range []HTTPSOptions{
		{ManifestURL: "http://updates.example/manifest", AllowedDownloadPrefix: "https://updates.example/releases/"},
		{ManifestURL: "https://user:secret@updates.example/manifest", AllowedDownloadPrefix: "https://updates.example/releases/"},
		{ManifestURL: "https://updates.example/manifest#fragment", AllowedDownloadPrefix: "https://updates.example/releases/"},
		{ManifestURL: "https://updates.example/manifest", AllowedDownloadPrefix: "https://updates.example/releases"},
	} {
		if _, err := NewHTTPSSource(options); !errors.Is(err, updates.ErrInvalid) {
			t.Errorf("NewHTTPSSource(%#v) = %v", options, err)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type contextBody struct{ ctx context.Context }

func (body contextBody) Read([]byte) (int, error) {
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}

func (contextBody) Close() error { return nil }

func serverURL(request *http.Request) string { return "https://" + request.Host }
