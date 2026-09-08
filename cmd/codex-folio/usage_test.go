package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
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
		TriggerReason: usage.TriggerExplicitRefresh,
		Observations:  []httpapi.UsageObservation{{MetricKey: "codex.primary.used_percent", Value: 0, Unit: "percent", Provenance: "Provider-reported Metric", Freshness: "stale", CaptureAgeSeconds: 900, Availability: "available"}},
		Availability:  []httpapi.UsageMetricAvailability{{MetricKey: "codex.primary.used_percent", State: "available"}, {MetricKey: "codex.secondary.used_percent", State: "unsupported", Reason: "capability_unsupported", CheckedAt: "2026-09-06T12:00:00Z"}},
	}
	var output bytes.Buffer
	writeUsageSnapshot(&output, result)
	got := output.String()
	for _, want := range []string{"Usage Snapshot: Work (partial)", "Trigger: explicit_refresh", "codex.primary.used_percent: 0 percent", "age 900s", "codex.secondary.used_percent: unsupported (capability_unsupported, checked 2026-09-06T12:00:00Z)", "Provider-reported Metric"} {
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

func TestUsageShowDefaultsToSelectedAndRequiresExplicitCombinedFlag(t *testing.T) {
	action, alias, scope, options, err := parseUsageRequest([]string{"show", "--json"})
	if err != nil || action != "show" || alias != "" || scope != "" || !options.json {
		t.Fatalf("selected request = %q/%q/%q/%#v/%v", action, alias, scope, options, err)
	}
	_, _, scope, _, err = parseUsageRequest([]string{"show", "--combined"})
	if err != nil || scope != usage.ScopeCombinedIdentity {
		t.Fatalf("combined request scope/error = %q/%v", scope, err)
	}
	if _, _, _, _, err := parseUsageRequest([]string{"show", "--combined", "--combined"}); err == nil {
		t.Fatal("duplicate --combined was accepted")
	}
}

func TestWriteAnalyticsPreservesPerProfileUsageAndActivityEvidence(t *testing.T) {
	result := httpapi.AnalyticsResponse{
		Scope: usage.ScopeCombinedIdentity, EligibleProfileCount: 2,
		Profiles: []httpapi.UsageSnapshotResponse{{Alias: "Personal", Status: usage.AvailabilityAvailable, Observations: []httpapi.UsageObservation{}, Availability: []httpapi.UsageMetricAvailability{}}},
		Activity: []httpapi.ActivityRecord{{RecordType: "observed_session", Id: "session-1", ProfileAlias: "Personal", Source: "local_metadata", SourceVersion: "v5", Provenance: activity.ProvenanceObservedSession, StartedAt: "2026-09-07T12:00:00Z", LastObservedAt: "2026-09-07T12:05:00Z", CorrelationState: activity.CorrelationCorrelated, CorrelationEvidenceType: "explicit", CorrelationConfidence: "high"}},
	}
	var output bytes.Buffer
	writeAnalytics(&output, result)
	for _, want := range []string{"Dashboard Scope: combined_identity", "Eligible Profile Count: 2", "Usage Snapshot: Personal", "Observed Session: session-1", "correlation correlated (explicit, high)"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("analytics output = %q, want %q", output.String(), want)
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
	home := t.TempDir()
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
	if _, err := stateStore.SaveDocumentedMetadata(ctx, "profile-1", profile.DocumentedMetadata{LoginIdentity: "login-shared", Workspace: "workspace-shared"}); err != nil {
		t.Fatal(err)
	}
	addProfile := func(id, alias string, metadata profile.DocumentedMetadata) {
		if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: id, Alias: alias, DisplayName: alias}); err != nil {
			t.Fatal(err)
		}
		if err := stateStore.SetManagedHome(ctx, id, "home-"+id, t.TempDir()); err != nil {
			t.Fatal(err)
		}
		for _, stage := range []profile.SetupStage{profile.StageDiscovery, profile.StageHome, profile.StageAuthentication, profile.StageValidation} {
			if err := stateStore.SaveSetupStage(ctx, id, stage); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := stateStore.PromotePendingProfile(ctx, id); err != nil {
			t.Fatal(err)
		}
		if metadata != (profile.DocumentedMetadata{}) {
			if _, err := stateStore.SaveDocumentedMetadata(ctx, id, metadata); err != nil {
				t.Fatal(err)
			}
		}
	}
	addProfile("profile-2", "SharedLogin", profile.DocumentedMetadata{LoginIdentity: "login-shared", Workspace: "workspace-other"})
	addProfile("profile-3", "SharedWorkspace", profile.DocumentedMetadata{LoginIdentity: "login-other", Workspace: "workspace-shared"})
	addProfile("profile-4", "MissingScope", profile.DocumentedMetadata{})
	if _, err := stateStore.SelectProfile(ctx, "Work"); err != nil {
		t.Fatal(err)
	}

	metric := usage.Registry()[0]
	tokenMetric, durationMetric := usage.Registry()[2], usage.Registry()[3]
	start := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	overlapStart, overlapEnd := start, start.Add(time.Hour)
	disjointStart, disjointEnd := overlapEnd, overlapEnd.Add(time.Hour)
	available := func(at time.Time, state string, observed ...usage.Metric) []usage.MetricAvailability {
		states := map[string]string{metric.Key: state}
		for _, item := range observed {
			states[item.Key] = usage.AvailabilityAvailable
		}
		result := make([]usage.MetricAvailability, 0, len(usage.Registry()))
		for _, item := range usage.Registry() {
			itemState := states[item.Key]
			if itemState == "" {
				itemState = usage.AvailabilityUnsupported
			}
			reason := ""
			if itemState == usage.AvailabilityUnsupported {
				reason = usage.ReasonUnsupported
			}
			result = append(result, usage.MetricAvailability{MetricKey: item.Key, State: itemState, Reason: reason, CheckedAt: at, Provenance: item.SourceClass})
		}
		return result
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
				{Metric: tokenMetric, Value: 10, ObservedAt: start.Add(13 * time.Minute), CapturedAt: start.Add(13 * time.Minute), WindowStart: &overlapStart, WindowEnd: &overlapEnd, WindowTimezone: "UTC", Source: usage.SourceLocalMetadata, SourceVersion: "v1", Provenance: usage.ProvenanceLocal, Availability: usage.AvailabilityAvailable},
				{Metric: durationMetric, Value: 60, ObservedAt: start.Add(13 * time.Minute), CapturedAt: start.Add(13 * time.Minute), WindowStart: &overlapStart, WindowEnd: &overlapEnd, WindowTimezone: "UTC", Source: usage.SourceLocalMetadata, SourceVersion: "v1", Provenance: usage.ProvenanceLocal, Availability: usage.AvailabilityAvailable},
			}, Availability: available(start.Add(13*time.Minute), usage.AvailabilityAvailable, tokenMetric, durationMetric)}},
		{err: apperrors.New(apperrors.UsageCollectionFailed, usage.ErrCollectionFailed)},
	}}
	clock := &composedUsageClock{now: start}
	workflow, err := usage.NewService(stateStore, collector, clock)
	if err != nil {
		t.Fatal(err)
	}
	commandService := &usageCommandService{workflow: workflow, resolver: launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(home, "codex"), Version: "0.153.4"}}, store: stateStore}
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
	if err != nil || contradictory.Status != usage.AvailabilityContradictory || len(contradictory.Observations) != 4 {
		t.Fatalf("contradictory API projection = %#v/%v", contradictory, err)
	}

	clock.now = start.Add(14 * time.Minute)
	failed, err := refreshUsageForOutput(ctx, client, "Work")
	if apperrors.Code(err) != apperrors.UsageCollectionFailed || failed.Status != usage.AvailabilityTemporarilyUnavailable || failed.Availability[0].Reason != usage.ReasonCollectionFailed || len(failed.Observations) != 4 {
		t.Fatalf("failed-refresh API projection = %#v/%v", failed, err)
	}
	generatedLatest, _, err := generatedClient.GetLatestUsage(ctx, "Work")
	if err != nil || generatedLatest.SnapshotId != failed.SnapshotId || len(generatedLatest.Observations) != 4 {
		t.Fatalf("generated latest-usage projection = %#v/%v", generatedLatest, err)
	}
	profileSnapshot := func(alias string, capturedAt time.Time, observations ...usage.Observation) {
		target, err := stateStore.ResolveUsageProfile(ctx, alias)
		if err != nil {
			t.Fatal(err)
		}
		observedMetrics := make([]usage.Metric, 0, len(observations))
		for index := range observations {
			observation := &observations[index]
			observation.ObservedAt, observation.CapturedAt = capturedAt, capturedAt
			observation.Source, observation.SourceVersion = usage.SourceLocalMetadata, "v1"
			observation.Provenance, observation.Freshness, observation.Availability = usage.ProvenanceLocal, usage.FreshnessFresh, usage.AvailabilityAvailable
			if observation.WindowStart != nil {
				observation.WindowTimezone = "UTC"
			}
			observedMetrics = append(observedMetrics, observation.Metric)
		}
		snapshot := usage.Snapshot{
			Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", CapturedAt: capturedAt, Status: usage.AvailabilityPartial, TriggerReason: usage.TriggerDashboardRefresh,
			Observations: observations, Availability: available(capturedAt, usage.AvailabilityUnsupported, observedMetrics...),
		}
		if _, err := stateStore.SaveUsageSnapshot(ctx, target, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	profileSnapshot("SharedLogin", start.Add(14*time.Minute),
		usage.Observation{Metric: tokenMetric, Value: 10, WindowStart: &overlapStart, WindowEnd: &overlapEnd},
		usage.Observation{Metric: durationMetric, Value: 15, WindowStart: &overlapStart, WindowEnd: &overlapEnd})
	profileSnapshot("SharedWorkspace", start.Add(14*time.Minute),
		usage.Observation{Metric: tokenMetric, Value: 5, WindowStart: &overlapStart, WindowEnd: &overlapEnd},
		usage.Observation{Metric: durationMetric, Value: 60, WindowStart: &disjointStart, WindowEnd: &disjointEnd})
	profileSnapshot("MissingScope", start.Add(14*time.Minute),
		usage.Observation{Metric: tokenMetric, Value: 7}, usage.Observation{Metric: durationMetric, Value: 8})

	combined, _, err := generatedClient.GetAnalytics(ctx, usage.ScopeCombinedIdentity)
	if err != nil || combined.Scope != usage.ScopeCombinedIdentity || combined.EligibleProfileCount != 4 || len(combined.Profiles) != 4 || len(combined.Aggregates) != 2 || combined.Aggregates[0].MetricKey != durationMetric.Key || combined.Aggregates[0].Value != 143 || combined.Aggregates[0].Unit != "seconds" || combined.Aggregates[1].MetricKey != tokenMetric.Key || combined.Aggregates[1].Value != 22 || combined.Aggregates[1].Unit != "tokens" || len(combined.Ambiguities) != 0 {
		t.Fatalf("generated combined analytics = %#v/%v", combined, err)
	}
	profileSnapshot("SharedLogin", start.Add(15*time.Minute),
		usage.Observation{Metric: tokenMetric, Value: 12, WindowStart: &overlapStart, WindowEnd: &overlapEnd},
		usage.Observation{Metric: durationMetric, Value: 15, WindowStart: &overlapStart, WindowEnd: &overlapEnd})
	clock.now = start.Add(15 * time.Minute)
	commandCombined, err := client.Analytics(ctx, usage.ScopeCombinedIdentity)
	if err != nil || len(commandCombined.Aggregates) != 1 || commandCombined.Aggregates[0].MetricKey != durationMetric.Key || commandCombined.Aggregates[0].Value != 143 || len(commandCombined.Ambiguities) != 1 || commandCombined.Ambiguities[0].MetricKey != tokenMetric.Key {
		t.Fatalf("contradictory combined analytics = %#v/%v", commandCombined, err)
	}
	var output bytes.Buffer
	writeAnalytics(&output, commandCombined)
	for _, want := range []string{"Dashboard Scope: combined_identity", "Usage Snapshot: Work", "Usage Snapshot: SharedLogin", "Usage Snapshot: SharedWorkspace", "Usage Snapshot: MissingScope", "Combined codex.local.session_duration: 143 seconds", "Combined codex.local.tokens_used: ambiguous"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("combined CLI projection = %q, want %q", output.String(), want)
		}
	}
	output.Reset()
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
	clock.now = start.Add(16 * time.Minute)
	workflow, err = usage.NewService(stateStore, &composedUsageCollector{fixtures: []composedUsageFixture{{err: apperrors.New(apperrors.UsageCollectionFailed, usage.ErrCollectionFailed)}}}, clock)
	if err != nil {
		t.Fatal(err)
	}
	commandService.workflow = workflow
	commandService.store = stateStore
	restarted, _, err := generatedClient.GetLatestUsage(ctx, "Work")
	if err != nil || restarted.Status != usage.AvailabilityTemporarilyUnavailable || len(restarted.Observations) != 4 {
		t.Fatalf("restart last-known API projection = %#v/%v", restarted, err)
	}
	restartedCombined, _, err := generatedClient.GetAnalytics(ctx, usage.ScopeCombinedIdentity)
	if err != nil || restartedCombined.EligibleProfileCount != 4 || len(restartedCombined.Profiles) != 4 || len(restartedCombined.Aggregates) != 1 || restartedCombined.Aggregates[0].Value != 143 || len(restartedCombined.Ambiguities) != 1 || restartedCombined.Ambiguities[0].MetricKey != tokenMetric.Key {
		t.Fatalf("restart combined analytics = %#v/%v", restartedCombined, err)
	}
	profiles, err := stateStore.ListEligibleProfiles(ctx)
	if err != nil || len(profiles) != 4 || !profiles[0].Selected || profiles[0].Alias != "Work" {
		t.Fatalf("Selected Profile after combined view = %#v/%v", profiles, err)
	}
}

func TestComposedRecommendationsUseTheSamePolicyInHumanJSONAndGeneratedAPI(t *testing.T) {
	for _, test := range []struct {
		name                                                                                         string
		age, otherAge                                                                                time.Duration
		change                                                                                       func(*usage.Snapshot)
		missingHome, missingCapacity, incompatibleCapability, failedRefresh, reauthenticationRefresh bool
		authStatus                                                                                   profile.Status
		winner                                                                                       string
		eligible                                                                                     int64
	}{
		{name: "unique winner", winner: "profile-1", eligible: 2},
		{name: "exact freshness boundary", age: 10 * time.Minute, winner: "profile-1", eligible: 2},
		{name: "stale higher capacity", age: 10*time.Minute + time.Second, winner: "profile-2", eligible: 2},
		{name: "stale only", age: 11 * time.Minute, otherAge: 11 * time.Minute, eligible: 2},
		{name: "tie", change: func(s *usage.Snapshot) { s.Observations[0].Value, s.Observations[1].Value = 30, 40 }, eligible: 2},
		{name: "crossed capacity", change: func(s *usage.Snapshot) { s.Observations[1].Value = 50 }, eligible: 2},
		{name: "partial provider evidence", change: func(s *usage.Snapshot) {
			s.Observations = s.Observations[:1]
			s.Availability[1].State = usage.AvailabilityUnsupported
		}, eligible: 2},
		{name: "mixed provenance", change: func(s *usage.Snapshot) { s.Observations[0].Provenance = usage.ProvenanceLocal }, eligible: 2},
		{name: "contradictory provider evidence", change: func(s *usage.Snapshot) {
			other := s.Observations[0]
			other.Value = 90
			s.Observations = append(s.Observations, other)
		}, eligible: 2},
		{name: "incompatible windows", change: func(s *usage.Snapshot) {
			start := s.Observations[0].WindowStart.Add(time.Hour)
			s.Observations[0].WindowStart = &start
		}, eligible: 2},
		{name: "missing capacity", missingCapacity: true, eligible: 2},
		{name: "failed refresh", failedRefresh: true, eligible: 2},
		{name: "reauthentication survives failed refresh", failedRefresh: true, reauthenticationRefresh: true, winner: "profile-2", eligible: 1},
		{name: "missing home", missingHome: true, winner: "profile-2", eligible: 1},
		{name: "unusable authentication", authStatus: profile.StatusNeedsReauthentication, winner: "profile-2", eligible: 1},
		{name: "unavailable lifecycle", authStatus: profile.StatusUnavailable, winner: "profile-2", eligible: 1},
		{name: "incompatible capability", incompatibleCapability: true, eligible: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			paths := launchTestPaths(t)
			secureVault := seedReadyLaunchProfile(t, paths)
			seedReferencedReadyProfile(t, paths, secureVault)
			stateStore, err := store.OpenWithVault(paths.DatabaseFile, secureVault)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stateStore.Close() }()
			// Pending profiles have neither authenticated homes nor launch eligibility.
			if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: "pending", Alias: "Pending", DisplayName: "Pending"}); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
			makeSnapshot := func(age time.Duration, values ...float64) usage.Snapshot {
				snapshot := usage.NewUnavailableSnapshot("0.153.4", now, usage.AvailabilityUnsupported, usage.ReasonUnsupported)
				for index, value := range values {
					start, end := now.Add(-12*time.Hour), now.Add(time.Duration(index+1)*24*time.Hour)
					metric := usage.Registry()[index]
					snapshot.Observations = append(snapshot.Observations, usage.Observation{Metric: metric, Value: value, CapturedAt: now.Add(-age), ObservedAt: now.Add(-age), WindowStart: &start, WindowEnd: &end, WindowTimezone: "UTC", Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Availability: usage.AvailabilityAvailable})
					snapshot.Availability[index].State, snapshot.Availability[index].Reason = usage.AvailabilityAvailable, ""
				}
				return snapshot
			}
			first, second := makeSnapshot(test.age, 10, 20), makeSnapshot(test.otherAge, 30, 40)
			if test.change != nil {
				test.change(&first)
			}
			fixtures := []composedUsageFixture{{snapshot: first}, {snapshot: second}}
			if test.reauthenticationRefresh {
				fixtures[0].snapshot = usage.NewUnavailableSnapshot("0.153.4", now, usage.AvailabilityReauthenticationRequired, usage.ReasonReauthentication)
			}
			if test.missingCapacity {
				fixtures = fixtures[1:]
			}
			collector := &composedUsageCollector{fixtures: fixtures}
			clock := &composedUsageClock{now: now}
			workflow, err := usage.NewService(stateStore, collector, clock)
			if err != nil {
				t.Fatal(err)
			}
			candidate := launch.Candidate{Path: filepath.Join(paths.Root, "codex"), Version: "0.153.4"}
			commandService := &usageCommandService{workflow: workflow, resolver: launchTestResolver{candidate: candidate}, store: stateStore}
			launches, err := newLaunchCommandService(stateStore, nil, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			server, err := httpapi.NewServer(httpapi.Options{Usage: commandService, Launches: launches, CommandToken: "ranking-test-token"})
			if err != nil {
				t.Fatal(err)
			}
			listener, err := server.Listen()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = server.Close() }()
			go func() { _ = server.Serve(listener) }()
			client := httpapi.NewCommandClient(server.Origin(), "ranking-test-token", nil)
			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			bootstrap, err := url.Parse(server.BootstrapURL())
			if err != nil {
				t.Fatal(err)
			}
			generated := httpapi.NewClient(server.Origin(), &composedBrowserDoer{client: &http.Client{Jar: jar}, origin: server.Origin()})
			if _, _, err := generated.ExchangeBootstrap(ctx, httpapi.BootstrapRequest{BootstrapToken: bootstrap.Query().Get("bootstrap")}); err != nil {
				t.Fatal(err)
			}
			if !test.missingCapacity {
				if _, err := client.RefreshUsage(ctx, "Work"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := client.RefreshUsage(ctx, "Personal"); err != nil {
				t.Fatal(err)
			}
			if test.failedRefresh {
				clock.now = now.Add(time.Second)
				collector.fixtures = []composedUsageFixture{{err: apperrors.New(apperrors.UsageCollectionFailed, usage.ErrCollectionFailed)}}
				if _, err := client.RefreshUsage(ctx, "Work"); apperrors.Code(err) != apperrors.UsageCollectionFailed {
					t.Fatalf("failed refresh = %v", err)
				}
			}
			if test.missingHome {
				if err := os.Rename(filepath.Join(paths.Root, "managed-home"), filepath.Join(paths.Root, "moved-home")); err != nil {
					t.Fatal(err)
				}
			}
			if test.authStatus != "" {
				if err := stateStore.SetAuthenticationState(ctx, "profile-1", test.authStatus, ""); err != nil {
					t.Fatal(err)
				}
			}
			if test.incompatibleCapability {
				commandService.resolver = launchTestResolver{}
			}
			for _, scope := range []string{"", usage.ScopeCombinedIdentity} {
				result, err := client.Analytics(ctx, scope)
				if err != nil {
					t.Fatal(err)
				}
				winner := test.winner
				if scope == "" && winner != "profile-1" {
					winner = ""
				}
				if result.RecommendedProfileId != winner || result.EligibleProfileCount != test.eligible {
					t.Fatalf("scope %q recommendation/count = %q/%d, want %q/%d", scope, result.RecommendedProfileId, result.EligibleProfileCount, winner, test.eligible)
				}
				browser, _, err := generated.GetAnalytics(ctx, scope)
				if err != nil || !reflect.DeepEqual(result, browser) {
					t.Fatalf("CLI/generated API mismatch: %#v/%#v/%v", result, browser, err)
				}
				var encoded, human bytes.Buffer
				if err := writeServiceJSON(&encoded, result); err != nil {
					t.Fatal(err)
				}
				var decoded httpapi.AnalyticsResponse
				if err := json.Unmarshal(encoded.Bytes(), &decoded); err != nil || !reflect.DeepEqual(result, decoded) {
					t.Fatalf("JSON policy mismatch: %#v/%v", decoded, err)
				}
				writeAnalytics(&human, result)
				if strings.Contains(human.String(), "Recommended") != (winner != "") {
					t.Fatalf("human recommendation = %q", human.String())
				}
				if test.age > 10*time.Minute && !strings.Contains(human.String(), "age "+strconv.FormatInt(int64(test.age/time.Second), 10)+"s") {
					t.Fatalf("stale exact age missing: %s", human.String())
				}
				for _, prohibited := range []string{paths.Root, "CODEX_HOME", "shared-login", "shared-workspace"} {
					if strings.Contains(encoded.String(), prohibited) || strings.Contains(human.String(), prohibited) {
						t.Fatalf("projection leaked %q", prohibited)
					}
				}
				if scope == usage.ScopeCombinedIdentity && (len(result.Candidates) != 3 || (test.age > 10*time.Minute && test.otherAge == 0 && result.Candidates[0].ProfileId != "profile-2")) {
					t.Fatalf("candidate visibility/order = %#v", result.Candidates)
				}
			}
			if test.name == "stale higher capacity" {
				prepared, err := client.Launch(ctx, httpapi.CommandLaunchRequest{Action: "prepare", Alias: "Work", Executable: candidate.Path, Version: candidate.Version, WorkingDirectory: paths.Root})
				if err != nil || prepared.Plan == nil {
					t.Fatalf("stale eligible launch blocked: %#v/%v", prepared, err)
				}
				collector.fixtures = []composedUsageFixture{{snapshot: makeSnapshot(0, 10, 20)}}
				clock.now = now.Add(time.Second)
				if _, err := client.RefreshUsage(ctx, "Work"); err != nil {
					t.Fatal(err)
				}
				result, err := client.Analytics(ctx, usage.ScopeCombinedIdentity)
				if err != nil || result.RecommendedProfileId != "profile-1" {
					t.Fatalf("manual refresh did not restore ranking: %#v/%v", result, err)
				}
			}
			if test.reauthenticationRefresh {
				collector.fixtures = []composedUsageFixture{{snapshot: first}}
				clock.now = now.Add(2 * time.Second)
				if _, err := client.RefreshUsage(ctx, "Work"); err != nil {
					t.Fatal(err)
				}
				result, err := client.Analytics(ctx, usage.ScopeCombinedIdentity)
				if err != nil || result.RecommendedProfileId != "profile-1" || result.EligibleProfileCount != 2 {
					t.Fatalf("successful authentication evidence did not restore ranking: %#v/%v", result, err)
				}
			}
			selected, err := stateStore.ListEligibleProfiles(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range selected {
				if item.Selected && item.Alias != "Work" {
					t.Fatalf("ranking changed selection: %#v", selected)
				}
			}
		})
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
