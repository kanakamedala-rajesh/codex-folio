package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/profile"
)

type testClock struct {
	now time.Time
}

func (clock *testClock) Now() time.Time {
	return clock.now
}

type diagnosticCapture struct {
	mu     sync.Mutex
	events []diagnostics.Event
}

type serviceLifecycleFixture struct {
	mu         sync.Mutex
	health     ServiceHealth
	unlockErr  error
	passphrase string
}

func (fixture *serviceLifecycleFixture) Health() ServiceHealth {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	result := fixture.health
	result.GuidanceCommands = append([]string(nil), result.GuidanceCommands...)
	return result
}

func (fixture *serviceLifecycleFixture) Unlock(_ context.Context, passphrase string) error {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.passphrase = passphrase
	if fixture.unlockErr != nil {
		return fixture.unlockErr
	}
	fixture.health = ServiceHealth{ServiceState: ServiceStateReady, VaultState: VaultStateUnlocked, DatabaseState: DatabaseStateReady}
	return nil
}

func (capture *diagnosticCapture) Record(event diagnostics.Event) error {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.events = append(capture.events, event)
	return nil
}

func (capture *diagnosticCapture) Events() []diagnostics.Event {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return append([]diagnostics.Event(nil), capture.events...)
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
	server, _, _ := startTestServer(t, Options{ServiceEnrollment: func() (string, string, bool) {
		return "not_installed", "systemd-user", true
	}})
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
	if got.GuidanceCommands == nil || len(got.GuidanceCommands) != 0 {
		t.Fatalf("metadata guidance_commands = %#v, want generated-contract empty array", got.GuidanceCommands)
	}
	if got.EnrollmentState != "not_installed" || got.EnrollmentMechanism != "systemd-user" || !got.EnrollmentAvailable {
		t.Fatalf("metadata enrollment = %#v", got)
	}
	if len(got.EnrollmentGuidance) != 2 || got.EnrollmentGuidance[0] != "codex-folio service status" {
		t.Fatalf("metadata enrollment guidance = %#v", got.EnrollmentGuidance)
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

func TestAuthorizedSelectionAPIReadsAndUpdatesSelectedProfile(t *testing.T) {
	repository := &selectionRepository{profiles: []profile.IdentityProfile{
		{ID: "profile-1", Alias: "Work", DisplayName: "Work", Status: profile.StatusReady, Selected: true},
		{ID: "profile-2", Alias: "Personal", DisplayName: "Personal", Status: profile.StatusReady},
	}}
	selector, err := profile.NewSelector(repository)
	if err != nil {
		t.Fatalf("NewSelector() error = %v", err)
	}
	registry, err := profile.NewRegistry(&registryRepository{profiles: repository.profiles})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	server, _, _ := startTestServer(t, Options{Selection: selector, Profiles: registry})
	client := testClient(t)
	origin := server.Origin()
	token := mustBootstrapToken(t, server.BootstrapURL())
	exchange, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+token+`"}`), "")
	if err != nil {
		t.Fatalf("bootstrap exchange: %v", err)
	}
	var bootstrap BootstrapResponse
	if err := json.NewDecoder(exchange.Body).Decode(&bootstrap); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	_ = exchange.Body.Close()

	current, err := doRequest(client, http.MethodGet, origin+SelectionPath, server.Address(), origin, nil, "")
	if err != nil {
		t.Fatalf("get selection: %v", err)
	}
	var currentSelection SelectionResponse
	if err := json.NewDecoder(current.Body).Decode(&currentSelection); err != nil {
		t.Fatalf("decode current selection: %v", err)
	}
	_ = current.Body.Close()
	if currentSelection.Alias != "Work" {
		t.Fatalf("current selection = %#v, want Work", currentSelection)
	}

	updated, err := doRequest(client, http.MethodPut, origin+SelectionPath, server.Address(), origin, []byte(`{"alias":"personal"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatalf("put selection: %v", err)
	}
	var updatedSelection SelectionResponse
	if err := json.NewDecoder(updated.Body).Decode(&updatedSelection); err != nil {
		t.Fatalf("decode updated selection: %v", err)
	}
	_ = updated.Body.Close()
	if updatedSelection.Alias != "Personal" || repository.profiles[1].Selected != true || repository.profiles[0].Selected {
		t.Fatalf("updated selection/repository = %#v/%#v, want Personal selected", updatedSelection, repository.profiles)
	}
}

func TestCommandProfileAPIRequiresAuthorizationAndReturnsSafeProjection(t *testing.T) {
	repository := &registryRepository{profiles: []profile.IdentityProfile{{
		ID: "profile-1", Alias: "Work", DisplayName: "Work", Email: "user@example.com", Workspace: "Example",
		Status: profile.StatusReady, IdentityHomeID: "home-1", IdentityHomePath: "/secret/home",
		IdentityHomeOwnership: profile.HomeOwnershipManaged, AuthenticationMethod: profile.AuthMethodBrowser, Selected: true,
	}}}
	registry, err := profile.NewRegistry(repository)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	server, _, _ := startTestServer(t, Options{Profiles: registry, CommandToken: "command-token"})
	if _, err := NewCommandClient(server.Origin(), "wrong-token", nil).ListProfiles(context.Background()); apperrors.Code(err) != apperrors.HTTPAPISessionInvalid {
		t.Fatalf("unauthorized ListProfiles() error = %v", err)
	}
	client := NewCommandClient(server.Origin(), "command-token", nil)
	result, err := client.ListProfiles(context.Background())
	if err != nil {
		t.Fatalf("ListProfiles() error = %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if len(result.Profiles) != 1 || result.Profiles[0].Alias != "Work" || result.Profiles[0].IdentityHomeID != "home-1" || strings.Contains(string(encoded), "secret/home") {
		t.Fatalf("safe profile projection = %s", encoded)
	}

	newAlias := "Personal"
	result, err = client.EditProfile(context.Background(), "Work", profile.ProfileEdits{Alias: &newAlias})
	if err != nil {
		t.Fatalf("EditProfile() error = %v", err)
	}
	encoded, err = json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if result.Updated == nil || result.Updated.ID != "profile-1" || result.Updated.Alias != newAlias || result.Updated.IdentityHomeID != "home-1" || result.Updated.IdentityHomeOwnership != profile.HomeOwnershipManaged || strings.Contains(string(encoded), "secret/home") {
		t.Fatalf("safe edited profile projection = %s", encoded)
	}
}

type registryRepository struct{ profiles []profile.IdentityProfile }

func (repository *registryRepository) ListProfiles(context.Context) ([]profile.IdentityProfile, error) {
	return append([]profile.IdentityProfile(nil), repository.profiles...), nil
}

func (repository *registryRepository) EditProfile(_ context.Context, alias string, edits profile.ProfileEdits) (profile.IdentityProfile, error) {
	for index := range repository.profiles {
		if !strings.EqualFold(repository.profiles[index].Alias, alias) {
			continue
		}
		if edits.Alias != nil {
			repository.profiles[index].Alias = *edits.Alias
		}
		if edits.DisplayName != nil {
			repository.profiles[index].DisplayName = *edits.DisplayName
		}
		if edits.Email != nil {
			repository.profiles[index].Email = *edits.Email
		}
		if edits.Workspace != nil {
			repository.profiles[index].Workspace = *edits.Workspace
		}
		return repository.profiles[index], nil
	}
	return profile.IdentityProfile{}, profile.ErrNotFound
}

type selectionRepository struct {
	profiles []profile.IdentityProfile
}

func (repository *selectionRepository) ListEligibleProfiles(context.Context) ([]profile.IdentityProfile, error) {
	return append([]profile.IdentityProfile(nil), repository.profiles...), nil
}

func (repository *selectionRepository) SelectProfile(_ context.Context, alias string) (profile.SelectionResult, error) {
	for index := range repository.profiles {
		if !strings.EqualFold(repository.profiles[index].Alias, alias) {
			continue
		}
		for other := range repository.profiles {
			repository.profiles[other].Selected = other == index
		}
		return profile.SelectionResult{Profile: repository.profiles[index]}, nil
	}
	return profile.SelectionResult{}, profile.ErrNotSelectable
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

func TestHTTPAuthorizationFailuresEmitStableRedactedDiagnostics(t *testing.T) {
	capture := &diagnosticCapture{}
	server, _, _ := startTestServer(t, Options{Diagnostics: capture})
	client := testClient(t)
	origin := server.Origin()
	token := mustBootstrapToken(t, server.BootstrapURL())

	before, err := doRequest(client, http.MethodGet, origin+MetadataPath, server.Address(), origin, nil, "")
	if err != nil {
		t.Fatalf("unbootstrapped request: %v", err)
	}
	assertErrorResponse(t, before, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid, token)

	hostAttack, err := doRequest(client, http.MethodGet, origin+MetadataPath, "localhost:"+serverPort(server), origin, nil, "")
	if err != nil {
		t.Fatalf("host attack: %v", err)
	}
	assertErrorResponse(t, hostAttack, http.StatusBadRequest, apperrors.HTTPAPIHostInvalid, token)

	originAttack, err := doRequest(client, http.MethodGet, origin+MetadataPath, server.Address(), "http://evil.example", nil, "")
	if err != nil {
		t.Fatalf("origin attack: %v", err)
	}
	assertErrorResponse(t, originAttack, http.StatusForbidden, apperrors.HTTPAPIOriginInvalid, token)

	exchange, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+token+`"}`), "")
	if err != nil {
		t.Fatalf("bootstrap exchange: %v", err)
	}
	var bootstrapResponse BootstrapResponse
	if err := json.NewDecoder(exchange.Body).Decode(&bootstrapResponse); err != nil {
		t.Fatalf("decode bootstrap response: %v", err)
	}
	_ = exchange.Body.Close()

	forgedCSRF, err := doRequest(client, http.MethodPost, origin+MetadataPath, server.Address(), origin, nil, "forged-csrf")
	if err != nil {
		t.Fatalf("forged CSRF request: %v", err)
	}
	assertErrorResponse(t, forgedCSRF, http.StatusForbidden, apperrors.HTTPAPICSRFInvalid, token)

	seen := make(map[string]diagnostics.Event)
	for _, event := range capture.Events() {
		if err := event.Validate(); err != nil {
			t.Fatalf("captured event %#v failed validation: %v", event, err)
		}
		seen[event.ErrorCode] = event
	}
	for _, code := range []string{
		apperrors.HTTPAPISessionInvalid,
		apperrors.HTTPAPIHostInvalid,
		apperrors.HTTPAPIOriginInvalid,
		apperrors.HTTPAPICSRFInvalid,
	} {
		event, ok := seen[code]
		if !ok {
			t.Fatalf("captured diagnostics = %#v, want code %q", capture.Events(), code)
		}
		if event.Component != diagnostics.ComponentHTTPAPI || event.Context == nil || event.Context.HTTPStatus == 0 {
			t.Fatalf("captured event = %#v, want HTTP component/status context", event)
		}
	}
	encoded, err := json.Marshal(capture.Events())
	if err != nil {
		t.Fatalf("json.Marshal(diagnostics) error = %v", err)
	}
	for _, forbidden := range []string{"bootstrap_token", token, "forged-csrf", "evil.example", "localhost"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("diagnostics %q contain forbidden value %q", encoded, forbidden)
		}
	}
}

func TestNewServerFailureEmitsSafePreListenerDiagnostic(t *testing.T) {
	capture := &diagnosticCapture{}
	_, err := NewServer(Options{Random: strings.NewReader(""), Diagnostics: capture})
	if err == nil || apperrors.Code(err) != apperrors.HTTPAPIServiceUnavailable {
		t.Fatalf("NewServer() error = %v, want stable service-unavailable error", err)
	}
	events := capture.Events()
	if len(events) != 1 || events[0].ErrorCode != apperrors.HTTPAPIServiceUnavailable || events[0].Context == nil || events[0].Context.State != diagnostics.StateUnavailable {
		t.Fatalf("pre-listener diagnostics = %#v, want bounded unavailable event", events)
	}
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

func TestCommandDashboardReentryRequiresCommandAuthorization(t *testing.T) {
	server, _, _ := startTestServer(t, Options{CommandToken: "dashboard-test-command"})
	client := testClient(t)
	old := mustBootstrapToken(t, server.BootstrapURL())
	response, err := doRequest(client, http.MethodPost, server.Origin()+"/api/v1/command/dashboard", server.Address(), server.Origin(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	assertErrorResponse(t, response, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid, "dashboard-test-command")
	command := NewCommandClient(server.Origin(), "dashboard-test-command", nil)
	link, err := command.Dashboard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	token := mustBootstrapToken(t, link)
	if token == old {
		t.Fatal("reentry reused the prior bootstrap")
	}
	body, _ := json.Marshal(BootstrapRequest{BootstrapToken: token})
	response, err = doRequest(client, http.MethodPost, server.Origin()+BootstrapPath, server.Address(), server.Origin(), body, "")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("reentry exchange: %d", response.StatusCode)
	}
	response.Body.Close()
	response, err = doRequest(client, http.MethodPost, server.Origin()+BootstrapPath, server.Address(), server.Origin(), body, "")
	if err != nil {
		t.Fatal(err)
	}
	assertErrorResponse(t, response, http.StatusUnauthorized, apperrors.HTTPAPIBootstrapInvalid, token)
}

func TestLockedServiceExposesSafeHealthAndCommandOnlyUnlock(t *testing.T) {
	const passphrase = "private unlock sentinel"
	lifecycle := &serviceLifecycleFixture{health: ServiceHealth{
		ServiceState:     ServiceStateLocked,
		VaultState:       VaultStateLocked,
		DatabaseState:    DatabaseStateNotChecked,
		GuidanceCommands: []string{"codex-folio vault unlock"},
	}}
	server, _, _ := startTestServer(t, Options{
		CommandToken:     "vault-command-token",
		ServiceLifecycle: lifecycle,
		StartLocked:      true,
	})
	command := NewCommandClient(server.Origin(), "vault-command-token", nil)
	health, err := command.ServiceHealth(context.Background())
	if err != nil {
		t.Fatalf("ServiceHealth() error = %v", err)
	}
	if health.ServiceState != ServiceStateLocked || health.VaultState != VaultStateLocked || health.DatabaseState != DatabaseStateNotChecked {
		t.Fatalf("locked health = %#v", health)
	}
	response, err := doRequest(testClient(t), http.MethodGet, server.Origin()+CommandAnalyticsPath, server.Address(), server.Origin(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	assertErrorResponse(t, response, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid, "vault-command-token")
	if _, err := command.Analytics(context.Background(), "combined_identity"); apperrors.Code(err) != apperrors.VaultLocked {
		t.Fatalf("locked analytics error = %v, want %s", err, apperrors.VaultLocked)
	}
	updated, err := command.UnlockVault(context.Background(), passphrase)
	if err != nil {
		t.Fatalf("UnlockVault() error = %v", err)
	}
	if updated.ServiceState != ServiceStateReady || updated.VaultState != VaultStateUnlocked || updated.DatabaseState != DatabaseStateReady {
		t.Fatalf("unlocked health = %#v", updated)
	}
	if lifecycle.passphrase != passphrase {
		t.Fatal("unlock lifecycle did not receive the private passphrase")
	}
	encoded, err := json.Marshal(updated)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(passphrase)) {
		t.Fatalf("health response contains passphrase: %s", encoded)
	}
}

func TestFailedVaultUnlockLeavesServiceLocked(t *testing.T) {
	lifecycle := &serviceLifecycleFixture{
		health:    ServiceHealth{ServiceState: ServiceStateLocked, VaultState: VaultStateLocked, DatabaseState: DatabaseStateNotChecked},
		unlockErr: apperrors.New(apperrors.VaultKeyInvalid, errors.New("sensitive provider cause")),
	}
	server, _, _ := startTestServer(t, Options{CommandToken: "vault-command-token", ServiceLifecycle: lifecycle, StartLocked: true})
	command := NewCommandClient(server.Origin(), "vault-command-token", nil)
	if _, err := command.UnlockVault(context.Background(), "wrong passphrase"); apperrors.Code(err) != apperrors.VaultKeyInvalid {
		t.Fatalf("UnlockVault() error = %v, want %s", err, apperrors.VaultKeyInvalid)
	}
	health, err := command.ServiceHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.ServiceState != ServiceStateLocked || health.VaultState != VaultStateLocked {
		t.Fatalf("health after failed unlock = %#v", health)
	}
}

func TestLockedBrowserMutationStillRequiresValidCSRF(t *testing.T) {
	lifecycle := &serviceLifecycleFixture{health: ServiceHealth{
		ServiceState:     ServiceStateLocked,
		VaultState:       VaultStateLocked,
		DatabaseState:    DatabaseStateNotChecked,
		GuidanceCommands: []string{"codex-folio vault unlock"},
	}}
	server, _, _ := startTestServer(t, Options{ServiceLifecycle: lifecycle, StartLocked: true})
	client := testClient(t)
	origin := server.Origin()
	token := mustBootstrapToken(t, server.BootstrapURL())
	exchange, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+token+`"}`), "")
	if err != nil {
		t.Fatalf("bootstrap exchange: %v", err)
	}
	var bootstrap BootstrapResponse
	if err := json.NewDecoder(exchange.Body).Decode(&bootstrap); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	_ = exchange.Body.Close()

	missing, err := doRequest(client, http.MethodPost, origin+UsageRefreshPath, server.Address(), origin, nil, "")
	if err != nil {
		t.Fatalf("locked mutation without CSRF: %v", err)
	}
	assertErrorResponse(t, missing, http.StatusForbidden, apperrors.HTTPAPICSRFInvalid, token)

	invalid, err := doRequest(client, http.MethodPost, origin+UsageRefreshPath, server.Address(), origin, nil, "invalid-csrf")
	if err != nil {
		t.Fatalf("locked mutation with invalid CSRF: %v", err)
	}
	assertErrorResponse(t, invalid, http.StatusForbidden, apperrors.HTTPAPICSRFInvalid, token)

	valid, err := doRequest(client, http.MethodPost, origin+UsageRefreshPath, server.Address(), origin, nil, bootstrap.CSRFToken)
	if err != nil {
		t.Fatalf("locked mutation with valid CSRF: %v", err)
	}
	assertErrorResponse(t, valid, http.StatusLocked, apperrors.VaultLocked, token)
}
