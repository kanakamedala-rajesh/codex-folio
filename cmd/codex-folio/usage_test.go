package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/usage"
	"venkatasudha.com/codex-folio/internal/vault"
)

func TestWriteUsageSnapshotReportsAvailabilityWithoutSensitivePaths(t *testing.T) {
	result := httpapi.UsageSnapshotResponse{
		SnapshotId: "snapshot-1", ProfileId: "profile-1", Alias: "Work", Source: "codex_app_server", SourceVersion: "0.153.4", CapturedAt: "2026-09-06T12:00:00Z", Status: "partial",
		Observations: []httpapi.UsageObservation{{MetricKey: "codex.primary.used_percent", Value: 0, Unit: "percent", Provenance: "Provider-reported Metric", Freshness: "stale", CaptureAgeSeconds: 900, Availability: "available"}},
		Availability: []httpapi.UsageMetricAvailability{{MetricKey: "codex.primary.used_percent", State: "available"}, {MetricKey: "codex.secondary.used_percent", State: "unsupported", Reason: "capability_unsupported", CheckedAt: "2026-09-06T12:00:00Z"}},
	}
	var output bytes.Buffer
	writeUsageSnapshot(&output, result)
	got := output.String()
	for _, want := range []string{"Usage Snapshot: Work (partial)", "codex.primary.used_percent: 0 percent", "age 900s", "codex.secondary.used_percent: unsupported (capability_unsupported, checked 2026-09-06T12:00:00Z)", "Provider-reported Metric"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output = %q, want %q", got, want)
		}
	}
	for _, prohibited := range []string{"CODEX_HOME", "/profiles/work", "cookie", "transcript"} {
		if strings.Contains(got, prohibited) {
			t.Fatalf("output contains prohibited value %q: %q", prohibited, got)
		}
	}
}

func TestComposedUsageFixturesPreserveHonestEvidenceAcrossRestart(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "usage.sqlite3")
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x39}, 32), "usage-generation")
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := store.OpenWithVault(databasePath, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "profile-1")
	if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: "profile-1", Alias: "Work", DisplayName: "Work"}); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.SetManagedHome(ctx, "profile-1", "home-1", home); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []profile.SetupStage{profile.StageDiscovery, profile.StageHome, profile.StageAuthentication, profile.StageValidation} {
		if err := stateStore.SaveSetupStage(ctx, "profile-1", stage); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := stateStore.PromotePendingProfile(ctx, "profile-1"); err != nil {
		t.Fatal(err)
	}

	metric, missingMetric := usage.Registry()[0], usage.Registry()[1]
	start := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	available := func(at time.Time, state string) []usage.MetricAvailability {
		return []usage.MetricAvailability{
			{MetricKey: metric.Key, State: state, CheckedAt: at, Provenance: usage.ProvenanceProvider},
			{MetricKey: missingMetric.Key, State: usage.AvailabilityUnsupported, CheckedAt: at, Provenance: usage.ProvenanceProvider},
		}
	}
	collector := &composedUsageCollector{fixtures: []composedUsageFixture{
		{snapshot: usage.Snapshot{Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: start,
			Observations: []usage.Observation{
				{Metric: metric, Value: 0, ObservedAt: start, CapturedAt: start, Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Availability: usage.AvailabilityAvailable},
				{Metric: metric, Value: 0, ObservedAt: start, CapturedAt: start, Source: usage.SourceLocalMetadata, SourceVersion: "v1", Provenance: usage.ProvenanceLocal, Availability: usage.AvailabilityAvailable},
				{Metric: metric, Value: 0, ObservedAt: start, CapturedAt: start, Source: usage.SourceDerived, SourceVersion: "v1", Provenance: usage.ProvenanceEstimated, Availability: usage.AvailabilityAvailable, Assumptions: "no concurrent activity"},
				{Metric: metric, Value: 0, ObservedAt: start, CapturedAt: start, Source: usage.SourceLocalMetadata, SourceVersion: "v1", Provenance: usage.ProvenanceObserved, Availability: usage.AvailabilityAvailable},
			}, Availability: available(start, usage.AvailabilityAvailable)}},
		{snapshot: usage.Snapshot{Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: start.Add(12 * time.Minute),
			Observations: []usage.Observation{{Metric: metric, Value: 0, ObservedAt: start, CapturedAt: start, Provenance: usage.ProvenanceProvider, Availability: usage.AvailabilityAvailable}}, Availability: available(start.Add(12*time.Minute), usage.AvailabilityAvailable)}},
		{snapshot: usage.Snapshot{Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: start.Add(13 * time.Minute),
			Observations: []usage.Observation{
				{Metric: metric, Value: 0, ObservedAt: start.Add(13 * time.Minute), CapturedAt: start.Add(13 * time.Minute), SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Availability: usage.AvailabilityAvailable},
				{Metric: metric, Value: 10, ObservedAt: start.Add(13 * time.Minute), CapturedAt: start.Add(13 * time.Minute), SourceVersion: "0.153.3", Provenance: usage.ProvenanceProvider, Availability: usage.AvailabilityAvailable},
			}, Availability: available(start.Add(13*time.Minute), usage.AvailabilityAvailable)}},
		{err: apperrors.New(apperrors.UsageCollectionFailed, usage.ErrCollectionFailed)},
	}}
	clock := &composedUsageClock{now: start}
	workflow, err := usage.NewService(stateStore, collector, clock)
	if err != nil {
		t.Fatal(err)
	}
	commandService := &usageCommandService{workflow: workflow, resolver: launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(home, "codex"), Version: "0.153.4"}}}
	server, err := httpapi.NewServer(httpapi.Options{Usage: commandService, CommandToken: "usage-token"})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	go func() { _ = server.Serve(listener) }()
	client := httpapi.NewCommandClient(server.Origin(), "usage-token", nil)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapURL, err := url.Parse(server.BootstrapURL())
	if err != nil {
		t.Fatal(err)
	}
	generatedClient := httpapi.NewClient(server.Origin(), &composedBrowserDoer{client: &http.Client{Jar: jar}, origin: server.Origin()})
	if _, _, err := generatedClient.ExchangeBootstrap(ctx, httpapi.BootstrapRequest{BootstrapToken: bootstrapURL.Query().Get("bootstrap")}); err != nil {
		t.Fatal(err)
	}

	first, err := client.RefreshUsage(ctx, "Work")
	if err != nil {
		t.Fatal(err)
	}
	labels := make(map[string]bool, len(first.Observations))
	for _, observation := range first.Observations {
		labels[observation.Provenance] = true
	}
	if first.Status != usage.AvailabilityPartial || first.Observations[0].Value != 0 || first.Availability[1].State != usage.AvailabilityUnsupported || !labels[usage.ProvenanceProvider] || !labels[usage.ProvenanceLocal] || !labels[usage.ProvenanceEstimated] || !labels[usage.ProvenanceObserved] {
		t.Fatalf("zero/missing/partial API projection = %#v", first)
	}

	clock.now = start.Add(12 * time.Minute)
	stale, err := client.RefreshUsage(ctx, "Work")
	if err != nil || stale.Observations[0].Freshness != usage.FreshnessStale || stale.Observations[0].CaptureAgeSeconds != 720 {
		t.Fatalf("stale API projection = %#v/%v", stale, err)
	}

	clock.now = start.Add(13 * time.Minute)
	contradictory, err := client.RefreshUsage(ctx, "Work")
	if err != nil || contradictory.Status != usage.AvailabilityContradictory || len(contradictory.Observations) != 2 {
		t.Fatalf("contradictory API projection = %#v/%v", contradictory, err)
	}

	clock.now = start.Add(14 * time.Minute)
	failed, err := refreshUsageForOutput(ctx, client, "Work")
	if apperrors.Code(err) != apperrors.UsageCollectionFailed || failed.Status != usage.AvailabilityTemporarilyUnavailable || failed.Availability[0].Reason != usage.ReasonCollectionFailed || len(failed.Observations) != 2 {
		t.Fatalf("failed-refresh API projection = %#v/%v", failed, err)
	}
	generatedLatest, _, err := generatedClient.GetLatestUsage(ctx, "Work")
	if err != nil || generatedLatest.SnapshotId != failed.SnapshotId || len(generatedLatest.Observations) != 2 {
		t.Fatalf("generated latest-usage projection = %#v/%v", generatedLatest, err)
	}
	var output bytes.Buffer
	writeUsageSnapshot(&output, failed)
	if !strings.Contains(output.String(), "refresh temporarily_unavailable: collection_failed") || !strings.Contains(output.String(), "codex.primary.used_percent: 0 percent") {
		t.Fatalf("last-known CLI projection = %q", output.String())
	}

	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}
	stateStore, err = store.OpenWithVault(databasePath, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stateStore.Close() }()
	clock.now = start.Add(15 * time.Minute)
	workflow, err = usage.NewService(stateStore, &composedUsageCollector{fixtures: []composedUsageFixture{{err: apperrors.New(apperrors.UsageCollectionFailed, usage.ErrCollectionFailed)}}}, clock)
	if err != nil {
		t.Fatal(err)
	}
	commandService.workflow = workflow
	restarted, _, err := generatedClient.GetLatestUsage(ctx, "Work")
	if err != nil || restarted.Status != usage.AvailabilityTemporarilyUnavailable || len(restarted.Observations) != 2 || restarted.Observations[0].Availability != usage.AvailabilityContradictory {
		t.Fatalf("restart last-known API projection = %#v/%v", restarted, err)
	}
}

type composedUsageFixture struct {
	snapshot usage.Snapshot
	err      error
}

type composedUsageCollector struct {
	fixtures []composedUsageFixture
}

func (collector *composedUsageCollector) Collect(_ context.Context, request usage.CollectionRequest) (usage.Snapshot, error) {
	fixture := collector.fixtures[0]
	collector.fixtures = collector.fixtures[1:]
	fixture.snapshot.CapturedAt = request.CapturedAt
	return fixture.snapshot, fixture.err
}

type composedUsageClock struct{ now time.Time }

func (clock *composedUsageClock) Now() time.Time { return clock.now }

type composedBrowserDoer struct {
	client *http.Client
	origin string
}

func (doer *composedBrowserDoer) Do(request *http.Request) (*http.Response, error) {
	request.Header.Set("Origin", doer.origin)
	return doer.client.Do(request)
}
