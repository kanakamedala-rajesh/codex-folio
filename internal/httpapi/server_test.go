package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

type testClock struct {
	now time.Time
}

func (clock *testClock) Now() time.Time {
	return clock.now
}

func TestServerBindsOnlyToLoopbackAndPublishesOneTimeURL(t *testing.T) {
	server, listener, _ := startTestServer(t, Options{})

	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !address.IP.IsLoopback() || address.IP.To4() == nil {
		t.Fatalf("listener address = %v, want IPv4 loopback", listener.Addr())
	}
	if server.Address() != listener.Addr().String() {
		t.Fatalf("Address() = %q, want %q", server.Address(), listener.Addr().String())
	}
	bootstrapURL := server.BootstrapURL()
	parsed, err := url.Parse(bootstrapURL)
	if err != nil {
		t.Fatalf("BootstrapURL() is invalid: %v", err)
	}
	if parsed.Scheme != "http" || parsed.Host != server.Address() || parsed.Path != BootstrapPathName {
		t.Fatalf("BootstrapURL() = %q, want loopback bootstrap URL", bootstrapURL)
	}
	if parsed.Query().Get(BootstrapQueryName) == "" {
		t.Fatalf("BootstrapURL() = %q, want one-time query value", bootstrapURL)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if server.BootstrapURL() != "" {
		t.Fatal("BootstrapURL() remains available after Close")
	}
}

func TestBootstrapExchangeAuthorizesMetadataAndCannotReplay(t *testing.T) {
	server, _, _ := startTestServer(t, Options{})
	client := testClient(t)
	bootstrapURL := server.BootstrapURL()
	origin := server.Origin()
	token := mustBootstrapToken(t, bootstrapURL)

	before, err := doRequest(client, http.MethodGet, origin+MetadataPath, server.Address(), origin, nil, "")
	if err != nil {
		t.Fatalf("unbootstrapped request: %v", err)
	}
	assertErrorResponse(t, before, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid, token)

	exchange, err := doRequest(
		client,
		http.MethodPost,
		origin+BootstrapPath,
		server.Address(),
		origin,
		[]byte(`{"bootstrap_token":"`+token+`"}`),
		"",
	)
	if err != nil {
		t.Fatalf("bootstrap exchange: %v", err)
	}
	if exchange.StatusCode != http.StatusOK {
		body := readBody(t, exchange)
		t.Fatalf("bootstrap status = %d, want %d; body = %q", exchange.StatusCode, http.StatusOK, body)
	}
	exchangeBody := readBodyBytes(t, exchange)
	var bootstrapResponse BootstrapResponse
	if err := json.Unmarshal(exchangeBody, &bootstrapResponse); err != nil {
		t.Fatalf("decode bootstrap response: %v", err)
	}
	if bootstrapResponse.CSRFToken == "" {
		t.Fatal("bootstrap response has empty CSRF token")
	}
	if server.BootstrapURL() != "" {
		t.Fatal("bootstrap URL remains available after exchange")
	}
	setCookie := exchange.Cookies()
	if len(setCookie) != 1 || setCookie[0].Name != SessionCookieName || !setCookie[0].HttpOnly || setCookie[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %#v, want one HttpOnly Strict cookie", setCookie)
	}
	if strings.Contains(string(exchangeBody), token) {
		t.Fatal("bootstrap response contains the bootstrap token")
	}

	metadata, err := doRequest(client, http.MethodGet, origin+MetadataPath, server.Address(), origin, nil, "")
	if err != nil {
		t.Fatalf("authorized metadata request: %v", err)
	}
	if metadata.StatusCode != http.StatusOK {
		body := readBody(t, metadata)
		t.Fatalf("metadata status = %d, want %d; body = %q", metadata.StatusCode, http.StatusOK, body)
	}
	var got MetadataResponse
	if err := json.NewDecoder(metadata.Body).Decode(&got); err != nil {
		t.Fatalf("decode metadata response: %v", err)
	}
	_ = metadata.Body.Close()
	if got.APIVersion != APIVersion || got.ContractVersion != ContractVersion {
		t.Fatalf("metadata = %#v, want API %q contract %q", got, APIVersion, ContractVersion)
	}

	replay, err := doRequest(
		http.DefaultClient,
		http.MethodPost,
		origin+BootstrapPath,
		server.Address(),
		origin,
		[]byte(`{"bootstrap_token":"`+token+`"}`),
		"",
	)
	if err != nil {
		t.Fatalf("bootstrap replay: %v", err)
	}
	assertErrorResponse(t, replay, http.StatusUnauthorized, apperrors.HTTPAPIBootstrapInvalid, token)
}

func TestHostOriginAndCSRFChecksFailClosedWithoutCORS(t *testing.T) {
	server, _, _ := startTestServer(t, Options{})
	client := testClient(t)
	origin := server.Origin()
	token := mustBootstrapToken(t, server.BootstrapURL())

	exchange, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+token+`"}`), "")
	if err != nil {
		t.Fatalf("bootstrap exchange: %v", err)
	}
	var bootstrapResponse BootstrapResponse
	if err := json.NewDecoder(exchange.Body).Decode(&bootstrapResponse); err != nil {
		t.Fatalf("decode bootstrap response: %v", err)
	}
	_ = exchange.Body.Close()

	hostAttack, err := doRequest(client, http.MethodGet, origin+MetadataPath, "localhost:"+serverPort(server), origin, nil, "")
	if err != nil {
		t.Fatalf("host attack: %v", err)
	}
	assertErrorResponse(t, hostAttack, http.StatusBadRequest, apperrors.HTTPAPIHostInvalid, token)
	assertNoCORS(t, hostAttack)

	originAttack, err := doRequest(client, http.MethodGet, origin+MetadataPath, server.Address(), "http://evil.example", nil, "")
	if err != nil {
		t.Fatalf("origin attack: %v", err)
	}
	assertErrorResponse(t, originAttack, http.StatusForbidden, apperrors.HTTPAPIOriginInvalid, token)
	assertNoCORS(t, originAttack)

	missingOrigin, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), "", []byte(`{"bootstrap_token":"invalid"}`), "")
	if err != nil {
		t.Fatalf("missing origin bootstrap: %v", err)
	}
	assertErrorResponse(t, missingOrigin, http.StatusForbidden, apperrors.HTTPAPIOriginInvalid, token)
	assertNoCORS(t, missingOrigin)

	forgedCSRF, err := doRequest(client, http.MethodPost, origin+MetadataPath, server.Address(), origin, nil, "forged")
	if err != nil {
		t.Fatalf("forged CSRF request: %v", err)
	}
	assertErrorResponse(t, forgedCSRF, http.StatusForbidden, apperrors.HTTPAPICSRFInvalid, token)
	assertNoCORS(t, forgedCSRF)

	validCSRFWrongMethod, err := doRequest(client, http.MethodPost, origin+MetadataPath, server.Address(), origin, nil, bootstrapResponse.CSRFToken)
	if err != nil {
		t.Fatalf("valid CSRF disallowed method: %v", err)
	}
	assertErrorResponse(t, validCSRFWrongMethod, http.StatusMethodNotAllowed, apperrors.HTTPAPIMethodNotAllowed, token)
	assertNoCORS(t, validCSRFWrongMethod)
}

func TestSessionExpiryAndRestartInvalidation(t *testing.T) {
	clock := &testClock{now: time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)}
	server, _, _ := startTestServer(t, Options{Clock: clock, BootstrapTTL: time.Minute, SessionTTL: time.Minute})
	client := testClient(t)
	origin := server.Origin()
	token := mustBootstrapToken(t, server.BootstrapURL())

	exchange, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+token+`"}`), "")
	if err != nil {
		t.Fatalf("bootstrap exchange: %v", err)
	}
	sessionCookies := exchange.Cookies()
	if len(sessionCookies) != 1 {
		t.Fatalf("session cookies = %#v, want one cookie", sessionCookies)
	}
	_ = exchange.Body.Close()
	clock.now = clock.now.Add(2 * time.Minute)
	expired, err := doRequest(client, http.MethodGet, origin+MetadataPath, server.Address(), origin, nil, "")
	if err != nil {
		t.Fatalf("expired session request: %v", err)
	}
	assertErrorResponse(t, expired, http.StatusUnauthorized, apperrors.HTTPAPISessionExpired, token)

	if err := server.Close(); err != nil {
		t.Fatalf("server restart close: %v", err)
	}
	restartedServer, _, _ := startTestServer(t, Options{Clock: clock, BootstrapTTL: time.Minute, SessionTTL: time.Minute})
	restartedOrigin := restartedServer.Origin()
	restartRequest, err := http.NewRequest(http.MethodGet, restartedOrigin+MetadataPath, nil)
	if err != nil {
		t.Fatalf("restart request: %v", err)
	}
	restartRequest.Host = restartedServer.Address()
	restartRequest.Header.Set("Origin", restartedOrigin)
	restartRequest.AddCookie(sessionCookies[0])
	invalidated, err := client.Do(restartRequest)
	if err != nil {
		t.Fatalf("restarted session request: %v", err)
	}
	assertErrorResponse(t, invalidated, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid, sessionCookies[0].Value)
}

func TestBootstrapExpiryIsSingleUseAndSafe(t *testing.T) {
	clock := &testClock{now: time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)}
	server, _, _ := startTestServer(t, Options{Clock: clock, BootstrapTTL: time.Minute})
	origin := server.Origin()
	token := mustBootstrapToken(t, server.BootstrapURL())
	clock.now = clock.now.Add(2 * time.Minute)

	response, err := doRequest(http.DefaultClient, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+token+`"}`), "")
	if err != nil {
		t.Fatalf("expired bootstrap: %v", err)
	}
	assertErrorResponse(t, response, http.StatusUnauthorized, apperrors.HTTPAPIBootstrapInvalid, token)
	if server.BootstrapURL() != "" {
		t.Fatal("expired bootstrap URL remains available after expiry")
	}
}

func TestEmbeddedShellHasRestrictiveOfflineHeadersAndAssets(t *testing.T) {
	server, _, _ := startTestServer(t, Options{})
	client := testClient(t)
	origin := server.Origin()

	page, err := doRequest(client, http.MethodGet, server.BootstrapURL(), server.Address(), "", nil, "")
	if err != nil {
		t.Fatalf("embedded shell: %v", err)
	}
	pageBody := readBody(t, page)
	if page.StatusCode != http.StatusOK || !strings.Contains(pageBody, "VenkataSudha CodexFolio") {
		t.Fatalf("shell status/body = %d/%q, want embedded product shell", page.StatusCode, pageBody)
	}
	csp := page.Header.Get("Content-Security-Policy")
	for _, required := range []string{"default-src 'self'", "script-src 'self'", "style-src 'self'", "object-src 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, required) {
			t.Errorf("CSP %q missing %q", csp, required)
		}
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "https:") {
		t.Fatalf("CSP = %q, want restrictive offline policy", csp)
	}

	for _, asset := range []string{"/assets/app.js", "/assets/styles.css"} {
		assetResponse, requestErr := doRequest(client, http.MethodGet, origin+asset, server.Address(), "", nil, "")
		if requestErr != nil {
			t.Fatalf("asset %s: %v", asset, requestErr)
		}
		if assetResponse.StatusCode != http.StatusOK {
			t.Fatalf("asset %s status = %d, want %d", asset, assetResponse.StatusCode, http.StatusOK)
		}
		if assetResponse.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("asset %s reflected CORS header", asset)
		}
		if asset == "/assets/app.js" {
			body := readBody(t, assetResponse)
			if !strings.Contains(body, "/api/v1/bootstrap") || !strings.Contains(body, "/api/v1/meta") {
				t.Fatal("embedded app bundle does not contain the generated API client calls")
			}
		} else {
			_ = assetResponse.Body.Close()
		}
	}
}

func startTestServer(t *testing.T, options Options) (*Server, net.Listener, <-chan error) {
	t.Helper()
	server, err := NewServer(options)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = server.Close()
		select {
		case <-serveDone:
		case <-time.After(time.Second):
			t.Errorf("Serve() did not stop after Close()")
		}
	})
	return server, listener, serveDone
}

func testClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error = %v", err)
	}
	return &http.Client{Jar: jar}
}

func doRequest(client *http.Client, method, requestURL, host, origin string, body []byte, csrf string) (*http.Response, error) {
	request, err := http.NewRequest(method, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Host = host
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		request.Header.Set(CSRFHeaderName, csrf)
	}
	return client.Do(request)
}

func mustBootstrapToken(t *testing.T, bootstrapURL string) string {
	t.Helper()
	parsed, err := url.Parse(bootstrapURL)
	if err != nil {
		t.Fatalf("parse bootstrap URL: %v", err)
	}
	encoded := parsed.Query().Get(BootstrapQueryName)
	if encoded == "" {
		t.Fatal("bootstrap URL has no token")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode bootstrap token: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(decoded)
}

func assertErrorResponse(t *testing.T, response *http.Response, status int, code, secret string) {
	t.Helper()
	body := readBody(t, response)
	if response.StatusCode != status {
		t.Fatalf("status = %d, want %d; body = %q", response.StatusCode, status, body)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("error body is not JSON: %v; body = %q", err, body)
	}
	if payload["code"] != code {
		t.Fatalf("error code = %q, want %q; body = %q", payload["code"], code, body)
	}
	if strings.Contains(body, secret) {
		t.Fatalf("error body contains secret %q: %q", secret, body)
	}
}

func assertNoCORS(t *testing.T, response *http.Response) {
	t.Helper()
	for _, header := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Access-Control-Allow-Methods", "Access-Control-Allow-Headers"} {
		if response.Header.Get(header) != "" {
			t.Errorf("response contains forbidden CORS header %s: %q", header, response.Header.Get(header))
		}
	}
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	body := readBodyBytes(t, response)
	return string(body)
}

func readBodyBytes(t *testing.T, response *http.Response) []byte {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	_ = response.Body.Close()
	return body
}

func serverPort(server *Server) string {
	return strings.TrimPrefix(server.Address(), "127.0.0.1:")
}
