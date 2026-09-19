package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
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
	code, output, diagnostic = run(append(append([]string{}, args...), "--dry-run"), "")
	if code != 0 || diagnostic != "" {
		t.Fatalf("second preview: %d/%s/%s", code, output, diagnostic)
	}
	if err := json.Unmarshal([]byte(output), &preview); err != nil {
		t.Fatal(err)
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
	target, err := state.ResolveUsageProfile(ctx, "Work")
	if err != nil {
		t.Fatal(err)
	}
	metric := usage.Registry()[0]
	capturedAt := clock.now.Add(-5 * time.Minute)
	snapshot := usage.Snapshot{Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: capturedAt, Status: usage.AvailabilityAvailable, TriggerReason: usage.TriggerExplicitRefresh,
		Observations: []usage.Observation{{Metric: metric, Value: 12.5, Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable, ObservedAt: capturedAt, CapturedAt: capturedAt, WindowTimezone: "UTC"}},
		Availability: completeComposedUsageAvailability(capturedAt, metric.Key)}
	if _, err := state.SaveUsageSnapshot(ctx, target, snapshot); err != nil {
		t.Fatal(err)
	}
	server, err := httpapi.NewServer(httpapi.Options{History: usage.NewHistoryService(state), Exports: activity.NewExportService(state), CommandToken: "history-test-token"})
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
	exportRequest := httpapi.AnalyticsExportRequest{Format: "json", Datasets: []string{"usage", "availability", "activity"}, Scope: usage.ScopeSelectedProfile, ProfileId: "selected", ProjectId: "*", From: "all", To: "all"}
	request = httpapi.HistoryRequest{Action: "export", Export: &exportRequest}
	exported, _, err := generated.ManageAnalyticsHistory(ctx, request)
	if err != nil || exported.Export == nil || exported.Export.SchemaVersion != activity.ExportSchemaVersion || len(exported.Export.Preview) != 3 || exported.Export.Records.Usage == nil || len(*exported.Export.Records.Usage) != 1 || (*exported.Export.Records.Usage)[0].Value != 12.5 || exported.Export.Records.Availability == nil || len(*exported.Export.Records.Availability) != len(usage.Registry()) {
		t.Fatalf("generated export = %#v/%v", exported.Export, err)
	}
	request = httpapi.HistoryRequest{Action: "purge", Scope: &httpapi.HistoryScope{ProfileId: "*", ProjectId: "*", From: "all", To: "all", Classes: []string{"usage"}}}
	preview, _, err := generated.ManageAnalyticsHistory(ctx, request)
	if err != nil || preview.Purge.Applied {
		t.Fatal("API dry run failed")
	}
	mismatched := "purge-mismatched"
	request.Confirmation = &mismatched
	if _, response, err := generated.ManageAnalyticsHistory(ctx, request); err == nil || response == nil || response.StatusCode != http.StatusConflict {
		t.Fatal("API accepted mismatched purge confirmation")
	}
	unchanged, _, err := generated.ManageAnalyticsHistory(ctx, httpapi.HistoryRequest{Action: "export", Export: &exportRequest})
	if err != nil || unchanged.Export == nil || unchanged.Export.Records.Usage == nil || len(*unchanged.Export.Records.Usage) != 1 {
		t.Fatal("mismatched purge changed analytics data")
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

func TestAnalyticsCLIExportsPreviewJSONAndCSVWithoutReplacingFiles(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	opener := func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
		return store.OpenWithVault(paths.DatabaseFile, secureVault)
	}
	run := func(args []string) (int, string, string) {
		var output, diagnostic bytes.Buffer
		code := runAnalyticsWithDependencies(args, strings.NewReader(""), &output, &diagnostic, func(*string) (platform.Paths, error) { return paths, nil }, opener, newServiceDiagnosticSink())
		return code, output.String(), diagnostic.String()
	}
	base := []string{"export", "--format", "json", "--datasets", "usage,activity", "--scope", "selected_profile", "--profile", "selected", "--project", "*", "--from", "all", "--to", "all", "--json"}
	code, output, diagnostic := run(append(append([]string{}, base...), "--dry-run"))
	if code != 0 || diagnostic != "" {
		t.Fatalf("preview = %d/%s/%s", code, output, diagnostic)
	}
	var preview analyticsExportPreview
	if err := json.Unmarshal([]byte(output), &preview); err != nil || preview.Filters.Scope != usage.ScopeSelectedProfile || preview.Filters.ProfileId != "selected" || preview.Filters.ProjectId != "*" || preview.Filters.From != "all" || preview.Filters.To != "all" || preview.Filters.IncludePaths || len(preview.Datasets) != 2 || preview.Datasets[0].Dataset != "usage" || preview.Datasets[0].RecordCount != 0 || len(preview.Datasets[0].Fields) == 0 {
		t.Fatalf("preview = %#v/%v", preview, err)
	}
	code, output, diagnostic = run(append(append([]string{}, base...), "--dry-run", "--include-paths"))
	if code != 0 || diagnostic != "" || json.Unmarshal([]byte(output), &preview) != nil || !preview.Filters.IncludePaths || !slices.Contains(preview.Datasets[0].Fields, "canonical_path") {
		t.Fatalf("path preview = %d/%#v/%s", code, preview, diagnostic)
	}
	destination := filepath.Join(t.TempDir(), "analytics.json")
	code, output, diagnostic = run(append(append([]string{}, base...), "--output", destination))
	if code != 0 || output == "" || diagnostic != "" {
		t.Fatalf("JSON export = %d/%s/%s", code, output, diagnostic)
	}
	encoded, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	var exported httpapi.AnalyticsExportResult
	if err := json.Unmarshal(encoded, &exported); err != nil || exported.SchemaVersion != activity.ExportSchemaVersion || exported.Records.Usage == nil || exported.Records.Activity == nil {
		t.Fatalf("exported JSON = %#v/%v", exported, err)
	}
	if err := os.WriteFile(destination, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	code, output, diagnostic = run(append(append([]string{}, base...), "--output", destination))
	kept, err := os.ReadFile(destination)
	if previewErr := json.Unmarshal([]byte(output), &preview); err != nil || previewErr != nil || code == 0 || string(kept) != "keep" || !strings.Contains(diagnostic, "CF_USAGE_ANALYTICS_EXPORT_FAILED") {
		t.Fatalf("existing destination = %d/%q/%q/%s/%v/%v", code, output, kept, diagnostic, err, previewErr)
	}
	csvDestination := filepath.Join(t.TempDir(), "activity.csv")
	csvArgs := []string{"export", "--format", "csv", "--datasets", "activity", "--scope", "selected_profile", "--profile", "selected", "--project", "*", "--from", "all", "--to", "all", "--output", csvDestination}
	if code, _, diagnostic = run(csvArgs); code != 0 {
		t.Fatalf("CSV export = %d/%s", code, diagnostic)
	}
	encoded, err = os.ReadFile(csvDestination)
	if err != nil || !strings.HasPrefix(string(encoded), "record_type,id,source_session_id,profile_id") {
		t.Fatalf("CSV = %q/%v", encoded, err)
	}
}

func TestAnalyticsCSVEncodesNormalizedDetailAndAggregateSemantics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "explicit", "project")
	metric := httpapi.HistoryMetric{MetricKey: "five_hour_used_percent", ValueKind: "percentage", Unit: "percent", Scope: "profile", Aggregation: "latest"}
	usageRecords := []httpapi.UsageExportRecord{{ObservationId: "observation-1", ProfileId: "profile-1", ProfileAlias: "Work", Metric: metric, Value: 12.5, Source: "codex_app_server", SourceVersion: "0.153.4", Provenance: "provider_reported", Freshness: "stale", Availability: "available", LoginIdentity: "login-1", Workspace: "workspace-1", ObservedAt: "2026-09-02T10:00:00Z", CapturedAt: "2026-09-02T10:00:00Z", CaptureAgeSeconds: 7200, CanonicalPath: &path}}
	aggregateRecords := []httpapi.HistoryAggregate{{Id: "aggregate-1", ProfileId: "profile-1", Metric: metric, Value: 37.5, Source: "codex_app_server", SourceVersion: "0.153.4", Provenance: "provider_reported", Availability: "available", BucketKind: "provider_window", BucketStart: "2026-09-02T00:00:00Z", BucketEnd: "2026-09-02T05:00:00Z", Timezone: "UTC", Samples: 3}}

	for _, test := range []struct {
		name, dataset, want string
		records             httpapi.AnalyticsExportRecords
	}{
		{name: "detail", dataset: "usage", want: "12.5", records: httpapi.AnalyticsExportRecords{Usage: &usageRecords}},
		{name: "aggregate", dataset: "aggregates", want: "37.5", records: httpapi.AnalyticsExportRecords{Aggregates: &aggregateRecords}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields := append([]string(nil), exportFieldsForTest(test.dataset)...)
			result := httpapi.AnalyticsExportResult{Filters: httpapi.AnalyticsExportRequest{Format: "csv", IncludePaths: test.dataset == "usage"}, Preview: []httpapi.AnalyticsExportDatasetPreview{{Dataset: test.dataset, Fields: fields}}, Records: test.records}
			encoded, err := encodeAnalyticsExport(result)
			rows, parseErr := csv.NewReader(strings.NewReader(string(encoded))).ReadAll()
			if err != nil || parseErr != nil || len(rows) != 2 || !slices.Contains(rows[1], test.want) {
				t.Fatalf("CSV = %q/%v/%v", encoded, err, parseErr)
			}
			if test.dataset == "usage" && (rows[0][len(rows[0])-1] != "canonical_path" || rows[1][len(rows[1])-1] != path) {
				t.Fatalf("explicit path CSV = %#v", rows)
			}
		})
	}
}

func TestAnalyticsCSVNeutralizesSpreadsheetFormulaCells(t *testing.T) {
	row := spreadsheetSafeCSVRow([]string{"=formula", "+command", "-value", "@reference", "safe"})
	if !slices.Equal(row, []string{"'=formula", "'+command", "'-value", "'@reference", "safe"}) {
		t.Fatalf("spreadsheet-safe row = %#v", row)
	}
}

func exportFieldsForTest(dataset string) []string {
	if dataset == "usage" {
		return append([]string{"observation_id", "profile_id", "profile_alias", "project_id", "project_alias", "project_basename", "metric_key", "value", "value_kind", "unit", "metric_scope", "aggregation", "source", "source_version", "provenance", "freshness", "availability", "login_identity", "workspace", "window_start", "window_end", "window_timezone", "observed_at", "captured_at", "capture_age_seconds", "assumptions", "uncertainty"}, "canonical_path")
	}
	return []string{"id", "profile_id", "profile_alias", "project_id", "project_alias", "project_basename", "metric_key", "value", "value_kind", "unit", "metric_scope", "aggregation", "source", "source_version", "provenance", "freshness", "availability", "login_identity", "workspace", "bucket_kind", "bucket_start", "bucket_end", "timezone", "first_observed_at", "last_observed_at", "first_captured_at", "last_captured_at", "samples", "assumptions", "uncertainty"}
}

type historyBrowserDoer struct {
	composedBrowserDoer
	csrf string
}

func completeComposedUsageAvailability(at time.Time, availableKey string) []usage.MetricAvailability {
	result := make([]usage.MetricAvailability, 0, len(usage.Registry()))
	for _, metric := range usage.Registry() {
		state, reason := usage.AvailabilityUnsupported, usage.ReasonUnsupported
		if metric.Key == availableKey {
			state, reason = usage.AvailabilityAvailable, ""
		}
		result = append(result, usage.MetricAvailability{MetricKey: metric.Key, State: state, Reason: reason, CheckedAt: at, Provenance: usage.ProvenanceProvider})
	}
	return result
}

func (doer *historyBrowserDoer) Do(request *http.Request) (*http.Response, error) {
	if doer.csrf != "" {
		request.Header.Set(httpapi.CSRFHeaderName, doer.csrf)
	}
	return doer.composedBrowserDoer.Do(request)
}
