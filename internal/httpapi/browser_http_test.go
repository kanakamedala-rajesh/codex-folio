package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"
)

type browserHTTPClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *browserHTTPClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *browserHTTPClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func browserHTTPToken(t *testing.T, link string) string {
	t.Helper()
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	token := fragment.Get(BootstrapQueryName)
	if parsed.Scheme != "http" || parsed.RawQuery != "" || token == "" {
		t.Fatal("browser link must carry bootstrap only in an HTTP URL fragment")
	}
	return token
}

func browserHTTPExchange(t *testing.T, s *Server, token string) BootstrapResponse {
	t.Helper()
	body, _ := json.Marshal(BootstrapRequest{BootstrapToken: token})
	response, err := doRequest(http.DefaultClient, http.MethodPost, s.Origin()+BootstrapPath, s.Address(), s.Origin(), body, "")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap status = %d", response.StatusCode)
	}
	if len(response.Header.Values("Set-Cookie")) != 0 {
		t.Fatal("HTTP bootstrap issued an ambient cookie")
	}
	var result BootstrapResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.SessionToken == nil || *result.SessionToken == "" || result.CSRFToken == "" {
		t.Fatal("bootstrap omitted session or CSRF material")
	}
	return result
}

func TestBrowserHTTPAuthorizationBoundaries(t *testing.T) {
	s, _, _ := startTestServer(t, Options{BrowserOnly: true})
	token := browserHTTPToken(t, s.BootstrapURL())
	session := browserHTTPExchange(t, s, token)
	cases := []struct {
		name, method, path string
		status             int
		change             func(*http.Request)
	}{
		{"header authorizes", http.MethodGet, MetadataPath, http.StatusOK, nil},
		{"missing header", http.MethodGet, MetadataPath, http.StatusUnauthorized, func(r *http.Request) { r.Header.Del(SessionHeaderName) }},
		{"cookie cannot authorize", http.MethodGet, MetadataPath, http.StatusUnauthorized, func(r *http.Request) {
			r.Header.Del(SessionHeaderName)
			r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: *session.SessionToken})
		}},
		{"duplicate header", http.MethodGet, MetadataPath, http.StatusUnauthorized, func(r *http.Request) { r.Header.Add(SessionHeaderName, *session.SessionToken) }},
		{"missing csrf", http.MethodPut, SelectionPath, http.StatusForbidden, nil},
		{"wrong csrf", http.MethodPut, SelectionPath, http.StatusForbidden, func(r *http.Request) { r.Header.Set(CSRFHeaderName, "incorrect") }},
		{"valid csrf reaches service", http.MethodPut, SelectionPath, http.StatusServiceUnavailable, func(r *http.Request) { r.Header.Set(CSRFHeaderName, session.CSRFToken) }},
		{"hostile host", http.MethodGet, MetadataPath, http.StatusBadRequest, func(r *http.Request) { r.Host = "attacker.example" }},
		{"hostile origin", http.MethodGet, MetadataPath, http.StatusForbidden, func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") }},
		{"private command denied", http.MethodPost, "/api/v1/command/dashboard", http.StatusNotFound, func(r *http.Request) { r.Header.Set(CommandTokenHeader, "test-command") }},
		{"persistent trust unavailable", http.MethodPost, BrowserTrustPath, http.StatusServiceUnavailable, func(r *http.Request) { r.Header.Set(CSRFHeaderName, session.CSRFToken) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request, err := http.NewRequest(tc.method, s.Origin()+tc.path, bytes.NewBufferString(`{"alias":"work","action":"grant"}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Origin", s.Origin())
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(SessionHeaderName, *session.SessionToken)
			if tc.change != nil {
				tc.change(request)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, tc.status)
			}
			if response.Header.Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("cross-origin access enabled")
			}
		})
	}
	body, _ := json.Marshal(BootstrapRequest{BootstrapToken: token})
	response, err := doRequest(http.DefaultClient, http.MethodPost, s.Origin()+BootstrapPath, s.Address(), s.Origin(), body, "")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bootstrap replay = %d", response.StatusCode)
	}
}

func TestBrowserHTTPSessionExpiresAndCannotAuthorizeReplacementOwner(t *testing.T) {
	clock := &browserHTTPClock{now: time.Now()}
	s, _, _ := startTestServer(t, Options{BrowserOnly: true, Clock: clock, SessionTTL: time.Minute})
	session := browserHTTPExchange(t, s, browserHTTPToken(t, s.BootstrapURL()))
	check := func(server *Server, want int) {
		t.Helper()
		request, _ := http.NewRequest(http.MethodGet, server.Origin()+MetadataPath, nil)
		request.Header.Set(SessionHeaderName, *session.SessionToken)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("session status = %d, want %d", response.StatusCode, want)
		}
	}
	check(s, http.StatusOK)
	replacement, _, _ := startTestServer(t, Options{BrowserOnly: true})
	check(replacement, http.StatusUnauthorized)
	clock.advance(time.Minute)
	check(s, http.StatusUnauthorized)
}

func TestPinnedCommandListenerReturnsHTTPLinkAndRejectsBrowserRoutes(t *testing.T) {
	browser, _, _ := startTestServer(t, Options{BrowserOnly: true})
	certificate, fingerprint := testLoopbackTLSCertificate(t)
	command, _, _ := startTestServer(t, Options{TLSCertificate: &certificate, CommandToken: "test-command", Dashboard: browser})
	client, err := NewPinnedCommandClient(command.Origin(), "test-command", fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	link, err := client.Dashboard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	session := browserHTTPExchange(t, browser, browserHTTPToken(t, link))
	for _, path := range []string{"/", BootstrapPath, MetadataPath, BrowserTrustPath} {
		request, _ := http.NewRequest(http.MethodGet, command.Origin()+path, nil)
		request.Header.Set(SessionHeaderName, *session.SessionToken)
		response, err := client.doer.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("command listener browser route %s = %d", path, response.StatusCode)
		}
	}
}
