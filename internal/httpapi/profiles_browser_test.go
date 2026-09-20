package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/profile"
)

type browserLifecycleRepository struct {
	record      profile.RemovalRecord
	replacement string
}

func (repository *browserLifecycleRepository) ListQuarantinedProfiles(context.Context) ([]profile.RemovalRecord, error) {
	return []profile.RemovalRecord{repository.record}, nil
}

func (repository *browserLifecycleRepository) BeginProfileRemoval(_ context.Context, _ string, replacement string) (profile.RemovalRecord, error) {
	repository.replacement = replacement
	return repository.record, nil
}
func (*browserLifecycleRepository) CompleteProfileQuarantine(context.Context, string) error {
	return nil
}
func (*browserLifecycleRepository) CancelProfileRemoval(context.Context, string) error { return nil }
func (repository *browserLifecycleRepository) GetProfile(context.Context, string) (profile.IdentityProfile, error) {
	return repository.record.Profile, nil
}
func (repository *browserLifecycleRepository) GetQuarantinedProfile(context.Context, string) (profile.RemovalRecord, error) {
	return repository.record, nil
}
func (*browserLifecycleRepository) RestoreProfile(context.Context, string) error { return nil }
func (*browserLifecycleRepository) PurgeProfile(context.Context, string) error   { return nil }

type browserHomeLifecycle struct{}

func (browserHomeLifecycle) Quarantine(context.Context, string, string) error { return nil }
func (browserHomeLifecycle) Restore(context.Context, string, string) error    { return nil }
func (browserHomeLifecycle) Purge(context.Context, string) error              { return nil }

const testProfilesPath = "/api/v1/profiles"

func TestBrowserProfileUsesUnboundedLastSuccessfulRefreshProjection(t *testing.T) {
	at := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	server := &Server{usage: &recordingUsageService{lastSuccess: at}}
	result, err := server.browserProfile(httptest.NewRequest(http.MethodGet, testProfilesPath, nil), profile.IdentityProfile{ID: "profile-1", Alias: "Work"})
	if err != nil {
		t.Fatal(err)
	}
	if result.LastSuccessfulRefresh != at.Format(time.RFC3339) {
		t.Fatalf("last successful refresh = %q, want %q", result.LastSuccessfulRefresh, at.Format(time.RFC3339))
	}
}

type browserProfileAuthenticationStub struct {
	request CommandProfileAuthenticationRequest
	status  profile.Status
}

func (service *browserProfileAuthenticationStub) Authenticate(_ context.Context, request CommandProfileAuthenticationRequest, output io.Writer) (CommandProfileAuthenticationResult, error) {
	service.request = request
	status := service.status
	if status == "" {
		status = profile.StatusReady
	}
	_, _ = io.WriteString(output, "device code: must-not-reach-browser\n")
	return CommandProfileAuthenticationResult{Setup: &profile.SetupResult{
		Profile: profile.IdentityProfile{
			ID: "profile-2", Alias: request.Alias, DisplayName: request.DisplayName,
			Status: status, IdentityHomeID: "home-2", IdentityHomePath: "/secret/new-home",
			IdentityHomeOwnership: profile.HomeOwnershipManaged, AuthenticationMethod: profile.AuthMethodBrowser,
		},
		Discovery:            profile.Discovery{Executable: "/secret/bin/codex", Version: "0.153.4"},
		Stages:               profile.SetupStages{Discovery: true, Home: true, Authentication: true, Validation: true, Selection: true},
		AuthenticationMethod: profile.AuthMethodBrowser,
	}}, nil
}

func TestAuthorizedProfilesAPIReturnsSafeInventoryAndMutatesThroughCSRF(t *testing.T) {
	repository := &registryRepository{profiles: []profile.IdentityProfile{
		{
			ID: "profile-1", Alias: "Work", DisplayName: "Work", Email: "user@example.com", Workspace: "Example",
			Status: profile.StatusReady, IdentityHomeID: "home-1", IdentityHomePath: "/secret/home",
			IdentityHomeOwnership: profile.HomeOwnershipReferenced, AuthenticationMethod: profile.AuthMethodBrowser, Selected: true,
		},
		{
			ID: "profile-pending", Alias: "Resume", DisplayName: "Resume", Status: profile.StatusPending,
			IdentityHomeID: "home-pending", IdentityHomePath: "/secret/pending", IdentityHomeOwnership: profile.HomeOwnershipReferenced,
		},
	}}
	registry, err := profile.NewRegistry(repository)
	if err != nil {
		t.Fatal(err)
	}
	authentication := &browserProfileAuthenticationStub{}
	server, _, _ := startTestServer(t, Options{Profiles: registry, ProfileAuthentication: authentication})
	client := testClient(t)
	origin := server.Origin()
	token := mustBootstrapToken(t, server.BootstrapURL())
	exchange, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+token+`"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	var bootstrap BootstrapResponse
	if err := json.NewDecoder(exchange.Body).Decode(&bootstrap); err != nil {
		t.Fatal(err)
	}
	_ = exchange.Body.Close()

	inventory, err := doRequest(client, http.MethodGet, origin+testProfilesPath, server.Address(), origin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, inventory)
	if inventory.StatusCode != http.StatusOK || !strings.Contains(body, `"alias":"Work"`) || !strings.Contains(body, `"identity_home_mode":"referenced"`) {
		t.Fatalf("inventory status/body = %d/%s", inventory.StatusCode, body)
	}
	for _, excluded := range []string{"home-1", "/secret/home", "identity_home_id", "identity_home_path"} {
		if strings.Contains(body, excluded) {
			t.Fatalf("inventory exposed %q: %s", excluded, body)
		}
	}

	withoutCSRF, err := doRequest(client, http.MethodPut, origin+testProfilesPath, server.Address(), origin, []byte(`{"alias":"Work","display_name":"Studio"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if withoutCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("PUT without CSRF status = %d, want %d", withoutCSRF.StatusCode, http.StatusForbidden)
	}
	_ = withoutCSRF.Body.Close()

	edited, err := doRequest(client, http.MethodPut, origin+testProfilesPath, server.Address(), origin, []byte(`{"alias":"Work","display_name":"Studio"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	editedBody := readBody(t, edited)
	if edited.StatusCode != http.StatusOK || !strings.Contains(editedBody, `"display_name":"Studio"`) {
		t.Fatalf("edited status/body = %d/%s", edited.StatusCode, editedBody)
	}
	invalidEdit, err := doRequest(client, http.MethodPut, origin+testProfilesPath, server.Address(), origin, []byte(`{"alias":" "}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	invalidEditBody := readBody(t, invalidEdit)
	if invalidEdit.StatusCode != http.StatusBadRequest || !strings.Contains(invalidEditBody, `"code":"CF_PROFILE_SETUP_INVALID"`) {
		t.Fatalf("invalid edit status/body = %d/%s", invalidEdit.StatusCode, invalidEditBody)
	}

	created, err := doRequest(client, http.MethodPost, origin+testProfilesPath, server.Address(), origin, []byte(`{"action":"add","alias":"Research","display_name":"Research","identity_home_mode":"managed","auth_method":"browser"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	createdBody := readBody(t, created)
	if created.StatusCode != http.StatusOK || !strings.Contains(createdBody, `"outcome":"ready"`) || !strings.Contains(createdBody, `"codex_version":"0.153.4"`) || !strings.Contains(createdBody, `"warnings":[]`) {
		t.Fatalf("created status/body = %d/%s", created.StatusCode, createdBody)
	}
	for _, excluded := range []string{"must-not-reach-browser", "/secret/bin/codex", "/secret/new-home", "home-2"} {
		if strings.Contains(createdBody, excluded) {
			t.Fatalf("authentication response exposed %q: %s", excluded, createdBody)
		}
	}
	if authentication.request.Action != "add" || authentication.request.AuthMethod != profile.AuthMethodBrowser || authentication.request.NonInteractive {
		t.Fatalf("authentication request = %#v", authentication.request)
	}

	missingHome, err := doRequest(client, http.MethodPost, origin+testProfilesPath, server.Address(), origin, []byte(`{"action":"add","alias":"Referenced","display_name":"Referenced","identity_home_mode":"referenced","auth_method":"browser"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	missingHomeBody := readBody(t, missingHome)
	if missingHome.StatusCode != http.StatusBadRequest || !strings.Contains(missingHomeBody, `"code":"CF_PROFILE_HOME_INVALID"`) {
		t.Fatalf("missing referenced home status/body = %d/%s", missingHome.StatusCode, missingHomeBody)
	}

	resumed, err := doRequest(client, http.MethodPost, origin+testProfilesPath, server.Address(), origin, []byte(`{"action":"add","alias":"Resume","display_name":"Resume","identity_home_mode":"referenced","auth_method":"browser"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.StatusCode != http.StatusOK {
		t.Fatalf("resumed status/body = %d/%s", resumed.StatusCode, readBody(t, resumed))
	}
	_ = resumed.Body.Close()

	completed, err := doRequest(client, http.MethodPost, origin+testProfilesPath, server.Address(), origin, []byte(`{"action":"add","alias":"Referenced","display_name":"Referenced","identity_home_mode":"referenced","referenced_home_path":"/existing","auth_method":"device-code","codex_override":"/opt/Codex Tools/codex"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	var completedResponse ProfileAuthenticationResponse
	if err := json.NewDecoder(completed.Body).Decode(&completedResponse); err != nil {
		t.Fatal(err)
	}
	_ = completed.Body.Close()
	if completed.StatusCode != http.StatusOK || completedResponse.Outcome != "ready" || completedResponse.TerminalCommand != "" {
		t.Fatalf("completed device status/response = %d/%#v", completed.StatusCode, completedResponse)
	}
	if authentication.request.Action != "add" || authentication.request.AuthMethod != "" || !authentication.request.NonInteractive {
		t.Fatalf("device preparation request = %#v", authentication.request)
	}

	authentication.status = profile.StatusPending
	prepared, err := doRequest(client, http.MethodPost, origin+testProfilesPath, server.Address(), origin, []byte(`{"action":"add","alias":"Device","display_name":"Device","identity_home_mode":"managed","auth_method":"device-code","codex_override":"/opt/Codex Tools/codex"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	var preparedResponse ProfileAuthenticationResponse
	if err := json.NewDecoder(prepared.Body).Decode(&preparedResponse); err != nil {
		t.Fatal(err)
	}
	_ = prepared.Body.Close()
	quotedOverride := `'--codex-bin=/opt/Codex Tools/codex'`
	if runtime.GOOS == "windows" {
		quotedOverride = `'--codex-bin=/opt/Codex Tools/codex'`
	}
	commandPrefix := ""
	if runtime.GOOS == "windows" {
		commandPrefix = "& "
	}
	wantPreparedCommand := commandPrefix + `codex-folio profile add Device --device-code ` + quotedOverride
	if prepared.StatusCode != http.StatusOK || preparedResponse.Outcome != "terminal_required" || preparedResponse.TerminalCommand != wantPreparedCommand {
		t.Fatalf("prepared status/response = %d/%#v", prepared.StatusCode, preparedResponse)
	}

	reauthenticated, err := doRequest(client, http.MethodPost, origin+testProfilesPath, server.Address(), origin, []byte(`{"action":"reauthenticate","alias":"Work","auth_method":"device-code","codex_override":"/opt/Codex Tools/codex"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	var reauthenticatedResponse ProfileAuthenticationResponse
	if err := json.NewDecoder(reauthenticated.Body).Decode(&reauthenticatedResponse); err != nil {
		t.Fatal(err)
	}
	_ = reauthenticated.Body.Close()
	wantReauthenticationCommand := commandPrefix + `codex-folio profile reauthenticate Work --device-code ` + quotedOverride
	if reauthenticated.StatusCode != http.StatusOK || reauthenticatedResponse.Outcome != "terminal_required" || reauthenticatedResponse.TerminalCommand != wantReauthenticationCommand {
		t.Fatalf("reauthenticated status/response = %d/%#v", reauthenticated.StatusCode, reauthenticatedResponse)
	}
}

func TestPowerShellTerminalCommandsPreserveLiteralArguments(t *testing.T) {
	arguments := []string{
		`C:\Program Files\Codex $tools\codex-folio.exe`,
		"profile", "add", "O'Brien", "--device-code",
		"--codex-bin=C:\\100%`$tools\\codex.exe",
		"--state-root=C:\\State $name\\folio",
	}
	want := `& 'C:\Program Files\Codex $tools\codex-folio.exe' profile add 'O''Brien' --device-code '--codex-bin=C:\100%` + "`" + `$tools\codex.exe' '--state-root=C:\State $name\folio'`
	if got := terminalCommandForOS("windows", arguments, true); got != want {
		t.Fatalf("PowerShell command = %q, want %q", got, want)
	}
	if got := terminalCommandForOS("windows", arguments[len(arguments)-1:], false); got != `'--state-root=C:\State $name\folio'` {
		t.Fatalf("PowerShell suffix = %q", got)
	}
}

func TestAuthorizedProfileLifecycleRequiresServerConfirmationAndReturnsSafeRecord(t *testing.T) {
	repository := &browserLifecycleRepository{record: profile.RemovalRecord{
		Profile: profile.IdentityProfile{
			ID: "profile-1", Alias: "Work", DisplayName: "Work", Status: profile.StatusReady,
			IdentityHomeOwnership: profile.HomeOwnershipManaged, IdentityHomePath: "/secret/home",
		},
		Action: profile.RemovalQuarantined, State: profile.QuarantineReady,
		QuarantinedAt: time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC),
		PurgeAfter:    time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC),
	}}
	lifecycle, err := profile.NewLifecycle(repository, browserHomeLifecycle{})
	if err != nil {
		t.Fatal(err)
	}
	server, _, _ := startTestServer(t, Options{ProfileLifecycle: lifecycle})
	client := testClient(t)
	origin := server.Origin()
	token := mustBootstrapToken(t, server.BootstrapURL())
	exchange, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+token+`"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	var bootstrap BootstrapResponse
	if err := json.NewDecoder(exchange.Body).Decode(&bootstrap); err != nil {
		t.Fatal(err)
	}
	_ = exchange.Body.Close()
	quarantine, err := doRequest(client, http.MethodGet, origin+ProfileLifecyclePath, server.Address(), origin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	quarantineBody := readBody(t, quarantine)
	if quarantine.StatusCode != http.StatusOK || !strings.Contains(quarantineBody, `"quarantined":[`) || !strings.Contains(quarantineBody, `"purge_after":"2026-09-18T10:00:00Z"`) {
		t.Fatalf("quarantine status/body = %d/%s", quarantine.StatusCode, quarantineBody)
	}
	withoutCSRF, err := doRequest(client, http.MethodPost, origin+ProfileLifecyclePath, server.Address(), origin, []byte(`{"action":"remove","alias":"Work","replacement":"Personal","confirmation":"Work"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if withoutCSRF.StatusCode != http.StatusForbidden || repository.replacement != "" {
		t.Fatalf("remove without CSRF status/replacement = %d/%q, want %d/empty", withoutCSRF.StatusCode, repository.replacement, http.StatusForbidden)
	}
	_ = withoutCSRF.Body.Close()

	mismatched, err := doRequest(client, http.MethodPost, origin+ProfileLifecyclePath, server.Address(), origin, []byte(`{"action":"remove","alias":"Work","replacement":"Personal","confirmation":"work"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	mismatchedBody := readBody(t, mismatched)
	if mismatched.StatusCode != http.StatusConflict || !strings.Contains(mismatchedBody, `"code":"CF_PROFILE_CONFIRMATION_INVALID"`) {
		t.Fatalf("mismatched confirmation status/body = %d/%s", mismatched.StatusCode, mismatchedBody)
	}

	removed, err := doRequest(client, http.MethodPost, origin+ProfileLifecyclePath, server.Address(), origin, []byte(`{"action":"remove","alias":"Work","replacement":"Personal","confirmation":"Work"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	removedBody := readBody(t, removed)
	if removed.StatusCode != http.StatusOK || !strings.Contains(removedBody, `"action":"quarantined"`) || !strings.Contains(removedBody, `"purge_after":"2026-09-18T10:00:00Z"`) || repository.replacement != "Personal" {
		t.Fatalf("remove status/body/replacement = %d/%s/%q", removed.StatusCode, removedBody, repository.replacement)
	}
	for _, excluded := range []string{"/secret/home", "identity_home_id", "identity_home_path"} {
		if strings.Contains(removedBody, excluded) {
			t.Fatalf("lifecycle response exposed %q: %s", excluded, removedBody)
		}
	}
}
