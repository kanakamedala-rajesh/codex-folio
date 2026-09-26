package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

func TestParseActivityRequestKeepsRefreshAndFiltersExplicit(t *testing.T) {
	refresh, _, err := parseActivityRequest([]string{"refresh", "Work", "--json"})
	if err != nil || refresh.Action != "refresh" || refresh.Alias != "Work" {
		t.Fatalf("refresh = %#v, %v", refresh, err)
	}
	list, _, err := parseActivityRequest([]string{"list", "--profile", "Work", "--project=project-1"})
	if err != nil || list.ProfileAlias != "Work" || list.ProjectID != "project-1" {
		t.Fatalf("list = %#v, %v", list, err)
	}
	for _, invalid := range [][]string{{"refresh", "Work", "--profile", "Work"}, {"list", "extra"}, {"list", "--profile"}} {
		if _, _, err := parseActivityRequest(invalid); err == nil {
			t.Fatalf("parseActivityRequest(%q) error = nil", invalid)
		}
	}
}

func TestActivityJSONUsesPublicAPIProjection(t *testing.T) {
	tokens := int64(42)
	records := []activity.TimelineRecord{{
		RecordType: activity.RecordTypeObservedSession, ID: "observed-1", SourceSessionID: "session-1",
		ProfileID: "profile-1", ProfileAlias: "Work", ProjectID: "project-1", ProjectAlias: "Folio",
		Source: "codex-local-state", SourceVersion: "5", Provenance: activity.ProvenanceObservedSession,
		StartedAt: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC), LastObservedAt: time.Date(2026, 9, 6, 12, 5, 0, 0, time.UTC),
		TokensUsed: &tokens, Correlation: activity.Correlation{State: activity.CorrelationCorrelated, ManagedLaunchID: "launch-1", EvidenceType: "explicit-session-id", Confidence: "high"},
	}}
	var output bytes.Buffer
	if err := writeActivityJSON(&output, records); err != nil {
		t.Fatal(err)
	}
	if want := mustJSON(t, httpapi.ActivityResponseFor(records)); output.String() != string(want) {
		t.Fatalf("CLI JSON = %s, API projection = %s", output.String(), want)
	}
}

func TestActivityServiceComposesSupportedReaderProjectAndStore(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	home := filepath.Join(paths.Root, "managed-home")
	repository := filepath.Join(home, "repository")
	if err := os.MkdirAll(filepath.Join(repository, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, created_at_ms INTEGER NOT NULL, updated_at_ms INTEGER NOT NULL, model TEXT, cwd TEXT NOT NULL, tokens_used INTEGER NOT NULL, private_prompt TEXT, future_unknown TEXT);
		INSERT INTO threads VALUES ('018f4f70-6f77-7c3f-9b77-93aa087dfc4d', 1788696000000, 1788696300000, 'gpt-5', ?, 42, 'prompt sentinel', 'credential sentinel')`, repository); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	projects, err := activity.NewProjectService(activity.ProjectServiceOptions{Repository: stateStore, Paths: platform.NewProjectPaths()})
	if err != nil {
		t.Fatal(err)
	}
	service, err := newActivityCommandService(stateStore, projects)
	if err != nil {
		t.Fatal(err)
	}
	before, err := service.Refresh(context.Background(), "Work")
	if err != nil || len(before) != 0 {
		t.Fatalf("refresh imported without consent: %#v, %v", before, err)
	}
	sources, err := service.ReviewSources(context.Background())
	if err != nil || len(sources) < 1 {
		t.Fatalf("sources = %#v, %v", sources, err)
	}
	if _, err := service.ImportSource(context.Background(), sources[0].SourceID, true); err != nil {
		t.Fatal(err)
	}
	timeline, err := service.Refresh(context.Background(), "Work")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if len(timeline) != 1 || timeline[0].RecordType != activity.RecordTypeObservedSession || timeline[0].ProjectAlias != "repository" || timeline[0].TokensUsed == nil || *timeline[0].TokensUsed != 42 {
		t.Fatalf("timeline = %#v", timeline)
	}
	encoded := string(mustJSON(t, timeline))
	for _, forbidden := range []string{repository, "prompt sentinel", "credential sentinel"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("timeline contains prohibited value %q: %s", forbidden, encoded)
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := writeServiceJSON(&output, value); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestActivityHumanProjectionNamesRecordBoundariesWithoutPaths(t *testing.T) {
	tokens, exitStatus := int64(42), 0
	records := []activity.TimelineRecord{
		{RecordType: activity.RecordTypeObservedSession, ID: "observed-1", ProfileAlias: "Work", ProjectAlias: "Folio", StartedAt: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC), LastObservedAt: time.Date(2026, 9, 6, 12, 5, 0, 0, time.UTC), Provenance: activity.ProvenanceObservedSession, Model: "gpt-5", TokensUsed: &tokens, Correlation: activity.Correlation{State: activity.CorrelationUncorrelated}},
		{RecordType: activity.RecordTypeManagedLaunch, ID: "launch-1", ProfileAlias: "Work", ProjectBasename: "codex-folio", StartedAt: time.Date(2026, 9, 6, 11, 0, 0, 0, time.UTC), LastObservedAt: time.Date(2026, 9, 6, 11, 5, 0, 0, time.UTC), Provenance: activity.ProvenanceManagedLaunch, Lifecycle: "exited", ExitStatus: &exitStatus, Correlation: activity.Correlation{State: activity.CorrelationCorrelated, EvidenceType: "explicit", Confidence: "high"}},
	}
	var output bytes.Buffer
	writeActivityTimeline(&output, records)
	text := output.String()
	for _, expected := range []string{"Observed Session: observed-1", "Managed Launch: launch-1", "correlation uncorrelated", "correlation correlated (explicit, high)", "tokens 42", "exited exit 0"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output %q does not contain %q", text, expected)
		}
	}
	if strings.Contains(text, "/private/") {
		t.Fatalf("output contains a path: %q", text)
	}
}
