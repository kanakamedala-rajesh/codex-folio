package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

// The browser receives these facts through the real supported reader, SQLite
// store, activity service and authenticated browser projection.
func seedDashboardActivity(t *testing.T, state *store.Store, projects *activity.ProjectService, project activity.ProjectIdentity, repository string) *activityCommandService {
	t.Helper()
	ctx := context.Background()
	const correlated = "018f4f70-6f77-7c3f-9b77-93aa087dfc4d"
	const contradictory = "018f4f70-6f77-7c3f-9b77-93aa087dfc4e"
	for _, fixture := range []struct{ alias, session string }{{"Work", correlated}, {"Personal", contradictory}} {
		plan, err := state.PrepareLaunch(ctx, launch.PrepareRequest{Alias: fixture.alias, Executable: filepath.Join(repository, "codex"), WorkingDirectory: repository, ProjectID: project.ID, ExpectedSessionID: fixture.session, Arguments: []string{"resume", fixture.session, "--", "command sentinel"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := state.MarkManagedLaunchStarted(ctx, plan.LeaseID, os.Getpid()); err != nil {
			t.Fatal(err)
		}
		if err := state.MarkManagedLaunchExited(ctx, plan.LeaseID, 17); err != nil {
			t.Fatal(err)
		}
	}
	otherRepository := filepath.Join(filepath.Dir(repository), "zephyr")
	if err := os.MkdirAll(filepath.Join(otherRepository, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := projects.Resolve(ctx, otherRepository, "Zephyr"); err != nil {
		t.Fatal(err)
	}
	fixtureSQL, err := os.ReadFile(filepath.Join("..", "..", "internal", "adapters", "codex", "testdata", "local-state", "v5", "threads.sql"))
	if err != nil {
		t.Fatal(err)
	}
	day := time.Now().UTC().Truncate(24 * time.Hour)
	for _, alias := range []string{"Work", "Personal"} {
		target, err := state.ResolveActivityProfile(ctx, alias)
		if err != nil {
			t.Fatal(err)
		}
		database, err := sql.Open("sqlite", filepath.Join(target.IdentityHome, "state_5.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(string(fixtureSQL)); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`DELETE FROM threads`); err != nil {
			t.Fatal(err)
		}
		type session struct {
			id, cwd string
			model   any
			tokens  int64
			start   time.Time
		}
		sessions := []session{{"018f4f70-6f77-7c3f-9b77-93aa087dfc50", otherRepository, "gpt-5", 84, day.Add(-45*24*time.Hour + 10*time.Hour)}}
		if alias == "Work" {
			sessions = []session{
				{correlated, repository, "gpt-5", 42, day.Add(-24*time.Hour + 10*time.Hour)},
				{contradictory, repository, "gpt-5", 21, day.Add(-24*time.Hour + 11*time.Hour)},
				{"018f4f70-6f77-7c3f-9b77-93aa087dfc4f", filepath.Join(otherRepository, "unavailable-project"), nil, 0, day.Add(-24*time.Hour + 12*time.Hour)},
			}
		}
		for _, record := range sessions {
			if _, err := database.Exec(`INSERT INTO threads (id, created_at_ms, updated_at_ms, source, model, cwd, tokens_used, title, preview, first_user_message, future_unknown) VALUES (?, ?, ?, 'cli', ?, ?, ?, 'private title', 'private preview', 'private prompt', 'credential sentinel')`, record.id, record.start.UnixMilli(), record.start.Add(5*time.Minute).UnixMilli(), record.model, record.cwd, record.tokens); err != nil {
				t.Fatal(err)
			}
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	}
	service, err := newActivityCommandService(state, projects)
	if err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"Work", "Personal"} {
		if _, err := service.Refresh(ctx, alias); err != nil {
			t.Fatal(err)
		}
	}
	return service
}

func TestDashboardActivityFixturePreservesIndependentMetadata(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	seedReferencedReadyProfile(t, paths, secureVault)
	state, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	projects, err := activity.NewProjectService(activity.ProjectServiceOptions{Repository: state, Paths: platform.NewProjectPaths()})
	if err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(filepath.Dir(paths.Root), "atlas")
	if err := os.MkdirAll(filepath.Join(repository, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	project, err := projects.Resolve(context.Background(), repository, "Atlas")
	if err != nil {
		t.Fatal(err)
	}
	service := seedDashboardActivity(t, state, projects, project, repository)
	records, err := service.List(context.Background(), activity.Filters{})
	if err != nil || len(records) != 6 {
		t.Fatalf("fixture records = %d, error = %v", len(records), err)
	}
	states := map[string]int{}
	foundOlderUnassigned := false
	for _, record := range records {
		states[record.Correlation.State]++
		if record.ProfileAlias == "Unassigned History" && record.TokensUsed != nil && *record.TokensUsed == 84 {
			age := time.Since(record.LastObservedAt)
			foundOlderUnassigned = age > 30*24*time.Hour && age < 90*24*time.Hour
		}
		if record.RecordType == activity.RecordTypeManagedLaunch && (record.Lifecycle != "exited" || record.ExitStatus == nil || *record.ExitStatus != 17 || record.Model != "" || record.TokensUsed != nil) {
			t.Fatalf("managed facts were conflated: %#v", record)
		}
		if record.SourceSessionID == "018f4f70-6f77-7c3f-9b77-93aa087dfc4f" && (record.ProjectID != "" || record.Model != "" || record.TokensUsed == nil || *record.TokensUsed != 0) {
			t.Fatalf("partial observation = %#v", record)
		}
	}
	if !foundOlderUnassigned {
		t.Fatal("fixture does not contain a supported Unassigned observation between 30 and 90 days old")
	}
	if states[activity.CorrelationCorrelated] != 4 || states[activity.CorrelationContradictory] != 0 || states[activity.CorrelationUncorrelated] != 2 {
		t.Fatalf("fixture correlations = %#v", states)
	}
	encoded := string(mustJSON(t, httpapi.ActivityResponseFor(records)))
	for _, forbidden := range []string{repository, "private title", "private preview", "private prompt", "credential sentinel", "command sentinel"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("browser projection contains prohibited value %q", forbidden)
		}
	}
}
