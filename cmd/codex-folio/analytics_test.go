package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/usage"
)

func TestAnalyticsCLIComposesRetentionAndConfirmedPurge(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	opener := func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
		return store.OpenWithVault(paths.DatabaseFile, secureVault)
	}
	state, err := opener(paths, "", "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := state.ResolveUsageProfile(context.Background(), "Work")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := usage.NewUnavailableSnapshot("0.153.4", time.Now().UTC(), usage.AvailabilityUnsupported, usage.ReasonUnsupported)
	snapshot.TriggerReason = usage.TriggerExplicitRefresh
	if _, err := state.SaveUsageSnapshot(context.Background(), target, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	run := func(args []string, input string) (int, string, string) {
		var output, diagnostic bytes.Buffer
		code := runAnalyticsWithDependencies(args, strings.NewReader(input), &output, &diagnostic, func(*string) (platform.Paths, error) { return paths, nil }, opener, newServiceDiagnosticSink())
		return code, output.String(), diagnostic.String()
	}
	code, output, diagnostic := run([]string{"retention", "--json"}, "")
	if code != 0 || !strings.Contains(output, `"setting":"13-months"`) || diagnostic != "" {
		t.Fatalf("default: %d/%s/%s", code, output, diagnostic)
	}
	code, output, diagnostic = run([]string{"retention", "unlimited", "--json"}, "")
	if code != 0 || !strings.Contains(output, `"setting":"unlimited"`) {
		t.Fatalf("setting: %d/%s/%s", code, output, diagnostic)
	}
	args := []string{"purge", "--profile", "*", "--project", "*", "--from", "all", "--to", "all", "--classes", "usage,aggregates", "--json"}
	code, output, diagnostic = run(append(append([]string{}, args...), "--dry-run"), "")
	if code != 0 || !strings.Contains(output, `"applied":false`) || !strings.Contains(output, `"confirmation":"purge-`) {
		t.Fatalf("preview: %d/%s/%s", code, output, diagnostic)
	}
	var preview httpapi.HistoryResponse
	if err := json.Unmarshal([]byte(output), &preview); err != nil {
		t.Fatal(err)
	}
	code, _, diagnostic = run(append(append([]string{}, args...), "--non-interactive"), "")
	if code == 0 || !strings.Contains(diagnostic, "CF_USAGE_ANALYTICS_CONFIRMATION_INVALID") {
		t.Fatalf("noninteractive: %d/%s", code, diagnostic)
	}
	code, _, diagnostic = run(args, "wrong\n")
	if code == 0 || !strings.Contains(diagnostic, "CF_USAGE_ANALYTICS_CONFIRMATION_INVALID") {
		t.Fatalf("interactive: %d/%s", code, diagnostic)
	}
	code, output, diagnostic = run(append(append([]string{}, args...), "--confirm", preview.Purge.Confirmation, "--non-interactive"), "")
	if code != 0 || !strings.Contains(output, `"applied":true`) || !strings.Contains(output, `"count":4`) {
		t.Fatalf("confirmed automation: %d/%s/%s", code, output, diagnostic)
	}
	code, output, diagnostic = run(args, preview.Purge.Confirmation+"\r\n")
	if code != 0 || !strings.Contains(output, `"applied":true`) || !strings.Contains(diagnostic, "Type purge-") {
		t.Fatalf("typed confirmation: %d/%s/%s", code, output, diagnostic)
	}
}

func TestGeneratedHistoryAPIUsesAuthorizedServiceAndRealStore(t *testing.T) {
	ctx := context.Background()
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	clock := &composedUsageClock{now: time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)}
	state, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	server, err := httpapi.NewServer(httpapi.Options{History: usage.NewHistoryService(state), CommandToken: "history-test-token"})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	doer := &historyBrowserDoer{composedBrowserDoer: composedBrowserDoer{client: &http.Client{Jar: jar}, origin: server.Origin()}}
	generated := httpapi.NewClient(server.Origin(), doer)
	request := httpapi.HistoryRequest{Action: "retention"}
	if _, response, err := generated.ManageAnalyticsHistory(ctx, request); err == nil || response.StatusCode < 400 {
		t.Fatal("unauthorized history access succeeded")
	}
	bootstrapURL, err := url.Parse(server.BootstrapURL())
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, _, err := generated.ExchangeBootstrap(ctx, httpapi.BootstrapRequest{BootstrapToken: bootstrapURL.Query().Get("bootstrap")})
	if err != nil {
		t.Fatal(err)
	}
	if _, response, err := generated.ManageAnalyticsHistory(ctx, request); err == nil || response.StatusCode != http.StatusForbidden {
		t.Fatal("missing CSRF accepted")
	}
	doer.csrf = bootstrap.CSRFToken
	command := httpapi.NewCommandClient(server.Origin(), "history-test-token", nil)
	cli, err := command.History(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	browser, _, err := generated.ManageAnalyticsHistory(ctx, request)
	if err != nil || !reflect.DeepEqual(cli, browser) {
		t.Fatalf("CLI/API mismatch: %#v/%#v/%v", cli, browser, err)
	}
	setting, run := "30", true
	request.Setting, request.Run = &setting, &run
	if _, _, err := generated.ManageAnalyticsHistory(ctx, request); err != nil {
		t.Fatal(err)
	}
	if policy, err := state.AnalyticsRetention(ctx); err != nil || policy.Days != 30 {
		t.Fatal("API setting did not persist")
	}
	request = httpapi.HistoryRequest{Action: "purge", Scope: &httpapi.HistoryScope{ProfileId: "*", ProjectId: "*", From: "all", To: "all", Classes: []string{"usage"}}}
	preview, _, err := generated.ManageAnalyticsHistory(ctx, request)
	if err != nil || preview.Purge.Applied {
		t.Fatal("API dry run failed")
	}
	request.Confirmation = &preview.Purge.Confirmation
	if result, _, err := generated.ManageAnalyticsHistory(ctx, request); err != nil || !result.Purge.Applied {
		t.Fatal("API explicit confirmation failed")
	}
	request.Scope.From = ""
	if _, _, err := generated.ManageAnalyticsHistory(ctx, request); err == nil {
		t.Fatal("API accepted incomplete purge scope")
	}
}

type historyBrowserDoer struct {
	composedBrowserDoer
	csrf string
}

func (doer *historyBrowserDoer) Do(request *http.Request) (*http.Response, error) {
	if doer.csrf != "" {
		request.Header.Set(httpapi.CSRFHeaderName, doer.csrf)
	}
	return doer.composedBrowserDoer.Do(request)
}
