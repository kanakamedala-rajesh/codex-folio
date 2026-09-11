package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/usage"
)

type dashboardClock struct{ control string }

func (clock dashboardClock) Now() time.Time {
	now := time.Now().UTC()
	mode, _ := os.ReadFile(clock.control)
	if string(mode) == "expired" {
		return now.Add(2 * time.Hour)
	}
	if string(mode) == "stale-seed" {
		return now.Add(-18 * time.Minute)
	}
	return now
}

// Invoked by the canonical browser gate after the embedded frontend is built.
// Ordinary Go tests retain their deterministic, browser-independent behavior.
func TestOverviewBrowser(t *testing.T) {
	if os.Getenv("CODEX_FOLIO_BROWSER_TEST") != "1" {
		t.Skip("canonical browser gate runs this journey after building embedded assets")
	}
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	personalHome := seedReferencedReadyProfile(t, paths, secureVault)
	control := filepath.Join(t.TempDir(), "scenario")
	if err := os.WriteFile(control, []byte("stale-seed"), 0600); err != nil {
		t.Fatal(err)
	}
	clock := dashboardClock{control: control}
	state, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	fixturePath := filepath.Join("..", "..", "internal", "adapters", "codex", "testdata", "app-server", "v2")
	collector := codexadapter.NewUsageCollectorWithCommandRunner(func(_ context.Context, _ string, _ []string, environment []string, stdin io.Reader, stdout, _ io.Writer) error {
		input, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		mode, err := os.ReadFile(control)
		if err != nil {
			return err
		}
		if string(mode) == "offline" {
			return io.ErrUnexpectedEOF
		}
		if bytes.Contains(input, []byte(`"method":"account/read"`)) {
			if string(mode) == "reauth" {
				_, err = io.WriteString(stdout, `{"id":2,"result":{"account":null}}`)
			} else {
				_, err = io.WriteString(stdout, `{"id":2,"result":{"account":{"type":"chatgpt"}}}`)
			}
			return err
		}
		file := "supported.json"
		if string(mode) == "missing" {
			file = "missing.json"
		}
		if string(mode) == "future" {
			file = "future-unknown.json"
		}
		raw, err := os.ReadFile(filepath.Join(fixturePath, file))
		if err != nil {
			return err
		}
		var envelope map[string]any
		if err = json.Unmarshal(raw, &envelope); err != nil {
			return err
		}
		if result, ok := envelope["result"].(map[string]any); ok {
			if limits, ok := result["rateLimits"].(map[string]any); ok {
				for _, window := range []string{"primary", "secondary"} {
					if value, ok := limits[window].(map[string]any); ok {
						duration := 300
						if window == "secondary" {
							duration = 10080
						}
						value["resetsAt"] = time.Now().UTC().Truncate(time.Hour).Add(time.Duration(duration) * time.Minute).Unix()
						if !strings.Contains(strings.Join(environment, "\n"), personalHome) {
							if window == "primary" {
								value["usedPercent"] = 35
							} else {
								value["usedPercent"] = 50
							}
						}
						if string(mode) == "zero" {
							value["usedPercent"] = 100
						}
					}
				}
				if string(mode) == "partial" {
					delete(limits, "secondary")
				}
			}
		}
		return json.NewEncoder(stdout).Encode(envelope)
	})
	workflow, err := usage.NewService(state, dashboardCollector{collector, control}, clock)
	if err != nil {
		t.Fatal(err)
	}
	service := &usageCommandService{workflow: workflow, resolver: launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "codex"), Version: "0.153.4"}}, store: state}
	for _, alias := range []string{"Work", "Personal"} {
		if _, err := service.Refresh(context.Background(), alias, usage.TriggerExplicitRefresh); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(control, []byte("offline"), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := state.PrepareLaunch(context.Background(), launch.PrepareRequest{Alias: "Work", Executable: filepath.Join(paths.Root, "codex"), WorkingDirectory: paths.Root})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkManagedLaunchStarted(context.Background(), plan.LeaseID, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	selector, err := profile.NewSelector(state)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := profile.NewRegistry(state)
	if err != nil {
		t.Fatal(err)
	}
	server, err := httpapi.NewServer(httpapi.Options{Clock: clock, Usage: service, Selection: selector, Profiles: registry, CommandToken: "browser-fixture-command"})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	runner := exec.Command("node", filepath.Join("..", "..", "web", "scripts", "browser-test.mjs"), server.BootstrapURL(), control)
	runner.Stdout = os.Stdout
	runner.Stderr = os.Stderr
	if err := runner.Run(); err != nil {
		t.Fatal("Overview browser journey failed:", err)
	}
	for _, alias := range []string{"Work", "Personal"} {
		if _, err := service.Refresh(context.Background(), alias, usage.TriggerExplicitRefresh); err != nil {
			t.Fatal(err)
		}
	}
	link, err := httpapi.NewCommandClient(server.Origin(), "browser-fixture-command", nil).Dashboard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	reentry := exec.Command("node", filepath.Join("..", "..", "web", "scripts", "browser-test.mjs"), link, control, "reentry")
	reentry.Stdout = os.Stdout
	reentry.Stderr = os.Stderr
	if err := reentry.Run(); err != nil {
		t.Fatal("reentry journey failed:", err)
	}
	selected, err := httpapi.NewCommandClient(server.Origin(), "browser-fixture-command", nil).ListProfiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range selected.Profiles {
		if p.Selected && p.Alias == "Personal" {
			found = true
		}
	}
	if !found {
		t.Fatal("browser selection was not persisted for CLI")
	}
	record, err := state.GetManagedLaunch(context.Background(), plan.LeaseID)
	if err != nil || record.ProfileAlias != "Work" || record.State != launch.StateRunning {
		t.Fatal("selection changed running launch")
	}
}

type dashboardCollector struct {
	delegate usage.Collector
	control  string
}

func (collector dashboardCollector) Collect(ctx context.Context, request usage.CollectionRequest) (usage.Snapshot, error) {
	snapshot, err := collector.delegate.Collect(ctx, request)
	mode, _ := os.ReadFile(collector.control)
	if err == nil && string(mode) == "contradictory" && len(snapshot.Observations) > 0 {
		other := snapshot.Observations[0]
		other.Value = 49
		snapshot.Observations = append(snapshot.Observations, other)
	}
	return snapshot, err
}
