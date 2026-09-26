package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

type trustMemory struct {
	mu      sync.Mutex
	digests map[string]bool
}

var testTrustCredentials sync.Map

func TestBrowserTrustIgnoresHostWideCookie(t *testing.T) {
	credential := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, randomTokenSize))
	digest := sha256.Sum256([]byte(credential))
	state := &trustMemory{digests: map[string]bool{string(digest[:]): true}}
	server := &Server{browserTrust: state}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:30001"+BrowserTrustPath, nil)
	request.AddCookie(&http.Cookie{Name: "codexfolio_trust", Value: credential})
	if _, valid, err := server.trustDigest(request); err != nil || valid {
		t.Fatalf("host-wide cookie valid = %v, err = %v", valid, err)
	}
	request.Header.Set(TrustHeaderName, credential)
	if _, valid, err := server.trustDigest(request); err != nil || !valid {
		t.Fatalf("origin client header valid = %v, err = %v", valid, err)
	}
}

func (memory *trustMemory) GrantBrowserTrust(_ context.Context, digest []byte, _ time.Time) error {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	memory.digests[string(digest)] = true
	return nil
}
func (memory *trustMemory) BrowserTrusted(_ context.Context, digest []byte) (bool, error) {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	return memory.digests[string(digest)], nil
}
func (memory *trustMemory) ForgetBrowser(_ context.Context, digest []byte) error {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	delete(memory.digests, string(digest))
	return nil
}
func (memory *trustMemory) RevokeAllBrowsers(_ context.Context) error {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	memory.digests = make(map[string]bool)
	return nil
}

func trustRequest(t *testing.T, client *http.Client, origin, action, csrf string, renewHeader bool) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, origin+BrowserTrustPath, bytes.NewBufferString(`{"action":"`+action+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", origin)
	request.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		request.Header.Set(CSRFHeaderName, csrf)
	}
	if renewHeader {
		request.Header.Set("X-CodexFolio-Renew", "1")
	}
	if credential, ok := testTrustCredentials.Load(client); ok {
		request.Header.Set(TrustHeaderName, credential.(string))
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if action == "grant" && response.StatusCode == http.StatusOK {
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		response.Body = io.NopCloser(bytes.NewReader(body))
		var granted BrowserTrustResponse
		if err := json.Unmarshal(body, &granted); err != nil {
			t.Fatal(err)
		}
		if granted.Credential != nil {
			testTrustCredentials.Store(client, *granted.Credential)
		}
	}
	if action == "forget" || action == "revoke_all" {
		testTrustCredentials.Delete(client)
	}
	return response
}

func bootstrapTrustTest(t *testing.T, client *http.Client, server *Server) string {
	t.Helper()
	token := mustBootstrapToken(t, server.BootstrapURL())
	response, err := doRequest(client, http.MethodPost, server.Origin()+BootstrapPath, server.Address(), server.Origin(), []byte(`{"bootstrap_token":"`+token+`"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap = %d", response.StatusCode)
	}
	var result BootstrapResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result.CSRFToken
}

func TestBrowserTrustRenewsAcrossExpiryAndServiceRestartAndRevokes(t *testing.T) {
	state := &trustMemory{digests: make(map[string]bool)}
	var err error
	clock := &testClock{now: time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)}
	server, _, _ := startTestServer(t, Options{BrowserTrust: state, Clock: clock, SessionTTL: time.Minute, CommandToken: "test-command"})
	client := testClient(t)
	csrf := bootstrapTrustTest(t, client, server)
	origin := server.Origin()
	newBrowser := testClient(t)
	response := trustRequest(t, newBrowser, origin, "renew", "", true)
	assertErrorResponse(t, response, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid, "never-visible-secret")
	response = trustRequest(t, client, origin, "grant", "", false)
	assertErrorResponse(t, response, http.StatusForbidden, apperrors.HTTPAPICSRFInvalid, "never-visible-secret")
	response = trustRequest(t, client, origin, "grant", csrf, false)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("grant = %d", response.StatusCode)
	}
	_ = response.Body.Close()
	for _, boundary := range []struct {
		host, origin string
		status       int
		code         string
	}{
		{"evil.example", origin, http.StatusBadRequest, apperrors.HTTPAPIHostInvalid},
		{server.Address(), "http://evil.example", http.StatusForbidden, apperrors.HTTPAPIOriginInvalid},
	} {
		request, err := http.NewRequest(http.MethodPost, origin+BrowserTrustPath, bytes.NewBufferString(`{"action":"renew"}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Host = boundary.host
		request.Header.Set("Origin", boundary.origin)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CodexFolio-Renew", "1")
		if credential, ok := testTrustCredentials.Load(client); ok {
			request.Header.Set(TrustHeaderName, credential.(string))
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		assertErrorResponse(t, response, boundary.status, boundary.code, "never-visible-secret")
	}
	clock.now = clock.now.Add(2 * time.Minute)
	response, err = doRequest(client, http.MethodGet, origin+MetadataPath, server.Address(), origin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	assertErrorResponse(t, response, http.StatusUnauthorized, apperrors.HTTPAPISessionExpired, "never-visible-secret")
	response = trustRequest(t, client, origin, "renew", "", false)
	assertErrorResponse(t, response, http.StatusForbidden, apperrors.HTTPAPICSRFInvalid, "never-visible-secret")
	response = trustRequest(t, client, origin, "renew", "", true)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("renew = %d", response.StatusCode)
	}
	var renewal BrowserTrustResponse
	if err := json.NewDecoder(response.Body).Decode(&renewal); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if !renewal.Trusted || renewal.CSRFToken == nil || *renewal.CSRFToken == "" {
		t.Fatalf("renewal = %#v", renewal)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, _, _ := startTestServer(t, Options{BrowserTrust: state, Clock: clock, SessionTTL: time.Minute, CommandToken: "test-command"})
	origin = restarted.Origin()
	response = trustRequest(t, client, origin, "renew", "", true)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("restart renew = %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&renewal); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	response = trustRequest(t, client, origin, "forget", *renewal.CSRFToken, false)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("forget = %d", response.StatusCode)
	}
	_ = response.Body.Close()
	response = trustRequest(t, client, origin, "renew", "", true)
	assertErrorResponse(t, response, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid, "never-visible-secret")
	response, err = doRequest(client, http.MethodGet, origin+MetadataPath, restarted.Address(), origin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	assertErrorResponse(t, response, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid, "never-visible-secret")
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	private := &http.Client{Jar: jar}
	response = trustRequest(t, private, origin, "renew", "", true)
	assertErrorResponse(t, response, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid, "never-visible-secret")
	command := NewCommandClient(origin, "test-command", nil)
	for _, browser := range []*http.Client{client, private} {
		link, err := command.Dashboard(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		token := mustBootstrapToken(t, link)
		exchange, err := doRequest(browser, http.MethodPost, origin+BootstrapPath, restarted.Address(), origin, []byte(`{"bootstrap_token":"`+token+`"}`), "")
		if err != nil {
			t.Fatal(err)
		}
		var auth BootstrapResponse
		if err := json.NewDecoder(exchange.Body).Decode(&auth); err != nil {
			t.Fatal(err)
		}
		_ = exchange.Body.Close()
		response = trustRequest(t, browser, origin, "grant", auth.CSRFToken, false)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("grant second browser = %d", response.StatusCode)
		}
		_ = response.Body.Close()
	}
	response = trustRequest(t, private, origin, "renew", "", true)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("second browser renew = %d", response.StatusCode)
	}
	var secondRenewal BrowserTrustResponse
	if err := json.NewDecoder(response.Body).Decode(&secondRenewal); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	response = trustRequest(t, private, origin, "revoke_all", *secondRenewal.CSRFToken, false)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("revoke all = %d", response.StatusCode)
	}
	_ = response.Body.Close()
	for _, browser := range []*http.Client{client, private} {
		response = trustRequest(t, browser, origin, "renew", "", true)
		assertErrorResponse(t, response, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid, "never-visible-secret")
		response, err = doRequest(browser, http.MethodGet, origin+MetadataPath, restarted.Address(), origin, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		assertErrorResponse(t, response, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid, "never-visible-secret")
	}
	// A fresh launcher link in an already trusted browser must bind its new
	// session to that trust, so Forget cannot leave it usable by cookie replay.
	for attempt := 0; attempt < 2; attempt++ {
		link, err := command.Dashboard(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		token := mustBootstrapToken(t, link)
		bootstrap, err := http.NewRequest(http.MethodPost, origin+BootstrapPath, bytes.NewBufferString(`{"bootstrap_token":"`+token+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		bootstrap.Header.Set("Content-Type", "application/json")
		bootstrap.Header.Set("Origin", origin)
		if credential, ok := testTrustCredentials.Load(client); ok {
			bootstrap.Header.Set(TrustHeaderName, credential.(string))
		}
		exchange, err := client.Do(bootstrap)
		if err != nil {
			t.Fatal(err)
		}
		var auth BootstrapResponse
		if err := json.NewDecoder(exchange.Body).Decode(&auth); err != nil {
			t.Fatal(err)
		}
		_ = exchange.Body.Close()
		if attempt == 0 {
			response = trustRequest(t, client, origin, "grant", auth.CSRFToken, false)
			if response.StatusCode != http.StatusOK {
				t.Fatalf("regrant = %d", response.StatusCode)
			}
			_ = response.Body.Close()
		} else {
			parsed, err := url.Parse(origin)
			if err != nil {
				t.Fatal(err)
			}
			var savedSession string
			for _, cookie := range client.Jar.Cookies(parsed) {
				if cookie.Name == SessionCookieName {
					savedSession = cookie.Value
				}
			}
			if savedSession == "" {
				t.Fatal("no bootstrap session cookie")
			}
			response = trustRequest(t, client, origin, "forget", auth.CSRFToken, false)
			if response.StatusCode != http.StatusOK {
				t.Fatalf("forget fresh bootstrap = %d", response.StatusCode)
			}
			_ = response.Body.Close()
			replay, err := http.NewRequest(http.MethodGet, origin+MetadataPath, nil)
			if err != nil {
				t.Fatal(err)
			}
			replay.Header.Set("Cookie", SessionCookieName+"="+savedSession)
			bare := &http.Client{}
			response, err = bare.Do(replay)
			if err != nil {
				t.Fatal(err)
			}
			assertErrorResponse(t, response, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid, "never-visible-secret")
		}
	}
}

func TestSessionIssuancePrunesExpiredSessions(t *testing.T) {
	server, err := NewServer(Options{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := range 20 {
		if _, err := server.issueSession(httptest.NewRecorder(), now.Add(time.Duration(i)*server.sessionTTL), nil); err != nil {
			t.Fatal(err)
		}
		if len(server.sessions) != 1 {
			t.Fatalf("retained %d sessions after expiry", len(server.sessions))
		}
	}
	// Issuing a second session before expiry must preserve other live browsers.
	if _, err := server.issueSession(httptest.NewRecorder(), now.Add(19*server.sessionTTL), nil); err != nil {
		t.Fatal(err)
	}
	if len(server.sessions) != 2 {
		t.Fatalf("live sessions = %d", len(server.sessions))
	}
}
