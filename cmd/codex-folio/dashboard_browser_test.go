package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/usage"
)

type dashboardClock struct{ control string }

type dashboardProfileAuthenticator struct{ control string }

func (authenticator dashboardProfileAuthenticator) Authenticate(_ context.Context, request profile.AuthenticationRequest) error {
	mode, _ := os.ReadFile(authenticator.control)
	if string(mode) == "profile-auth-fail" {
		return profile.ErrAuthenticationFailed
	}
	_, _ = io.WriteString(request.Stdout, "browser-auth-secret-must-not-reach-dashboard")
	if string(mode) == "profile-needs-auth" {
		return os.WriteFile(authenticator.control, []byte("supported"), 0600)
	}
	return nil
}

func (authenticator dashboardProfileAuthenticator) Check(_ context.Context, _ profile.AuthenticationRequest) error {
	mode, _ := os.ReadFile(authenticator.control)
	if string(mode) == "profile-needs-auth" {
		return profile.ErrNotAuthenticated
	}
	return nil
}

func (dashboardProfileAuthenticator) ObserveDocumentedMetadata(_ context.Context, request profile.AuthenticationRequest) (profile.DocumentedMetadata, error) {
	return profile.DocumentedMetadata{LoginIdentity: filepath.Base(request.IdentityHome) + "@example.test", Workspace: "Browser fixture"}, nil
}

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
	suite := os.Getenv("CODEX_FOLIO_BROWSER_SUITE")
	if suite == "" {
		suite = "deep"
	}
	if suite != "deep" && suite != "smoke" {
		t.Fatalf("unknown browser suite %q", suite)
	}
	runOverviewBrowser(t, suite)
}

func TestOverviewStartupBenchmark(t *testing.T) {
	if os.Getenv("CODEX_FOLIO_BROWSER_TEST") != "1" {
		t.Skip("canonical web gate runs the isolated startup benchmark")
	}
	var samples []float64
	var report map[string]any
	for i := 0; i < 6; i++ {
		if err := json.Unmarshal(runOverviewBrowser(t, "benchmark"), &report); err != nil {
			t.Fatal(err)
		}
		elapsed, ok := report["cachedEvidenceMs"].(float64)
		if !ok || elapsed <= 0 {
			t.Fatal("benchmark did not report a positive startup measurement")
		}
		if i > 0 {
			samples = append(samples, elapsed)
		}
	}
	sorted := slices.Clone(samples)
	slices.Sort(sorted)
	median := sorted[len(sorted)/2]
	delete(report, "cachedEvidenceMs")
	report["samplesMs"], report["medianMs"], report["maxMs"] = samples, median, sorted[len(sorted)-1]
	report["warmups"], report["budgetMs"] = 1, 1000
	report["method"] = "five serial isolated starts after one warm-up; median budget; fresh browser and SQLite state per start"
	output := os.Getenv("CODEX_FOLIO_BROWSER_OUTPUT")
	if output == "" {
		output = filepath.Join(os.TempDir(), "codex-folio-overview-browser")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "startup-benchmark.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("cached startup samples=%v ms median=%.1f ms max=%.1f ms budget=1000ms", samples, median, sorted[len(sorted)-1])
	if median >= 1000 {
		t.Fatalf("cached evidence median %.1fms exceeds 1s engineering budget", median)
	}
}

func writeDashboardFakeCodex(t *testing.T, control string) (string, string) {
	t.Helper()
	directory := t.TempDir()
	logPath := filepath.Join(directory, "launch.log")
	t.Setenv("CODEX_FOLIO_TEST_CONTROL", control)
	t.Setenv("CODEX_FOLIO_TEST_LAUNCH_LOG", logPath)
	executable := filepath.Join(directory, "codex")
	content := "#!/bin/sh\n{ pwd; printf '%s\\n' \"$@\"; } > \"$CODEX_FOLIO_TEST_LAUNCH_LOG\"\nwhile [ \"$(cat \"$CODEX_FOLIO_TEST_CONTROL\")\" != \"launch-exit-23\" ]; do sleep 0.01; done\nexit 23\n"
	if runtime.GOOS == "windows" {
		executable += ".cmd"
		content = "@echo off\r\n> \"%CODEX_FOLIO_TEST_LAUNCH_LOG%\" echo %CD%\r\n:args\r\nif \"%~1\"==\"\" goto wait\r\n>> \"%CODEX_FOLIO_TEST_LAUNCH_LOG%\" echo %~1\r\nshift\r\ngoto args\r\n:wait\r\nset \"launch_mode=\"\r\nset /p launch_mode=<\"%CODEX_FOLIO_TEST_CONTROL%\"\r\nif not \"%launch_mode%\"==\"launch-exit-23\" (\r\n  >nul ping 127.0.0.1 -n 2\r\n  goto wait\r\n)\r\nexit /b 23\r\n"
	}
	if err := os.WriteFile(executable, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	return executable, logPath
}

func runOverviewBrowser(t *testing.T, suite string) []byte {
	t.Helper()
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	personalHome := seedReferencedReadyProfile(t, paths, secureVault)
	referencedMarker := filepath.Join(personalHome, "browser-removal-marker")
	if err := os.WriteFile(referencedMarker, []byte("externally owned"), 0600); err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(t.TempDir(), "scenario")
	if err := os.WriteFile(control, []byte("stale-seed"), 0600); err != nil {
		t.Fatal(err)
	}
	fakeCodex, launchLog := writeDashboardFakeCodex(t, control)
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
	repository := filepath.Join(filepath.Dir(paths.Root), "atlas")
	if err := os.MkdirAll(filepath.Join(repository, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	projects, err := activity.NewProjectService(activity.ProjectServiceOptions{Repository: state, Paths: platform.NewProjectPaths()})
	if err != nil {
		t.Fatal(err)
	}
	project, err := projects.Resolve(context.Background(), repository, "Atlas")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := state.PrepareLaunch(context.Background(), launch.PrepareRequest{Alias: "Work", Executable: filepath.Join(paths.Root, "codex"), WorkingDirectory: repository, ProjectID: project.ID})
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
	profileAuthentication, err := newProfileAuthenticationCommandService(
		paths,
		state,
		nil,
		launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "codex"), Version: "0.153.4"}},
		dashboardProfileAuthenticator{control: control},
	)
	if err != nil {
		t.Fatal(err)
	}
	launches, err := newLaunchCommandService(state, nil, dashboardProfileAuthenticator{control: control}, projects, service)
	if err != nil {
		t.Fatal(err)
	}
	referencedHome := filepath.Join(filepath.Dir(paths.Root), "dashboard-referenced-home")
	if err := os.MkdirAll(referencedHome, 0700); err != nil {
		t.Fatal(err)
	}
	profileLifecycle, err := newProfileLifecycle(paths, state)
	if err != nil {
		t.Fatal(err)
	}
	server, err := httpapi.NewServer(httpapi.Options{Clock: clock, Usage: service, Selection: selector, Profiles: registry, ProfileLifecycle: profileLifecycle, ProfileAuthentication: profileAuthentication, Launches: launches, Projects: projects, CommandToken: "browser-fixture-command"})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: "browser-fixture-command"}); err != nil {
		t.Fatal(err)
	}
	stopLaunches := make(chan struct{})
	defer close(stopLaunches)
	launchErrors := make(chan error, 1)
	warmLaunchOverhead := make(chan time.Duration, 1)
	go func() {
		last := ""
		for {
			select {
			case <-stopLaunches:
				return
			case <-time.After(10 * time.Millisecond):
			}
			mode, readErr := os.ReadFile(control)
			if readErr != nil || string(mode) == last {
				continue
			}
			last = string(mode)
			if last != "launch-run-23" && last != "launch-fail" {
				continue
			}
			executable := fakeCodex
			if last == "launch-fail" {
				executable += ".missing"
			}
			startedAt := time.Now()
			result := make(chan int, 1)
			go func() {
				result <- runLaunchWithInputAndDependenciesAndOwnerOptions(
					[]string{"Personal", "--project", project.ID, "--", "--model", "gpt-5"}, strings.NewReader(""), io.Discard, io.Discard,
					func(*string) (platform.Paths, error) { return paths, nil },
					launchTestResolver{candidate: launch.Candidate{Path: executable, Version: "0.153.4"}}, nil, newForegroundProcess, nil, platform.OwnerOptions{},
				)
			}()
			if last == "launch-run-23" {
				for {
					records, listErr := state.ListActivity(context.Background(), activity.Filters{ProfileAlias: "Personal", ProjectID: project.ID})
					if listErr != nil {
						launchErrors <- listErr
						return
					}
					if slices.ContainsFunc(records, func(record activity.TimelineRecord) bool { return record.Lifecycle == string(launch.StateRunning) }) {
						warmLaunchOverhead <- time.Since(startedAt)
						break
					}
					time.Sleep(time.Millisecond)
				}
			}
			code := <-result
			if last == "launch-run-23" && code != 23 || last == "launch-fail" && code != exitFailure {
				launchErrors <- fmt.Errorf("unexpected browser fixture launch result %d", code)
				return
			}
		}
	}()
	runner := exec.Command("node", filepath.Join("..", "..", "web", "scripts", "browser-test.mjs"), server.BootstrapURL(), control, suite, referencedHome)
	output := t.TempDir()
	if suite == "benchmark" {
		runner.Env = append(os.Environ(), "CODEX_FOLIO_BROWSER_OUTPUT="+output)
	}
	runner.Stdout = os.Stdout
	runner.Stderr = os.Stderr
	runnerErr := runner.Run()
	if suite == "deep" {
		select {
		case elapsed := <-warmLaunchOverhead:
			t.Logf("warm managed-launch overhead %s: warm loopback service and CLI entry through persisted successful process-start reporting on native %s/%s; canonical non-host checks remain compile-only", elapsed, runtime.GOOS, runtime.GOARCH)
			if elapsed >= 300*time.Millisecond {
				t.Fatalf("warm managed-launch overhead = %s, want < 300ms", elapsed)
			}
		default:
			if runnerErr == nil {
				t.Fatal("warm managed-launch overhead was not measured")
			}
		}
	}
	if runnerErr != nil {
		t.Fatal("Overview browser journey failed:", runnerErr)
	}
	if content, err := os.ReadFile(referencedMarker); err != nil || string(content) != "externally owned" {
		t.Fatalf("referenced Identity Home marker changed: %q, %v", content, err)
	}
	select {
	case launchErr := <-launchErrors:
		t.Fatal("browser launch fixture failed:", launchErr)
	default:
	}
	if suite == "deep" {
		content, err := os.ReadFile(launchLog)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(content), "\r\n", "\n")), "\n")
		if len(lines) != 3 || filepath.Clean(lines[0]) != filepath.Clean(repository) || !slices.Equal(lines[1:], []string{"--model", "gpt-5"}) {
			t.Fatalf("native fake Codex launch = %q, want working directory %q and transported arguments", content, repository)
		}
	}
	if suite == "benchmark" {
		result, err := os.ReadFile(filepath.Join(output, "benchmark-results.json"))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	aliases := []string{"Work", "Personal"}
	if suite == "deep" {
		aliases = []string{"Work"}
	}
	for _, alias := range aliases {
		if _, err := service.Refresh(context.Background(), alias, usage.TriggerExplicitRefresh); err != nil {
			t.Fatal(err)
		}
	}
	link, err := httpapi.NewCommandClient(server.Origin(), "browser-fixture-command", nil).Dashboard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	reentry := exec.Command("node", filepath.Join("..", "..", "web", "scripts", "browser-test.mjs"), link, control, "reentry", referencedHome)
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
	expectedSelected := "Personal"
	if suite == "deep" {
		expectedSelected = "Work"
	}
	for _, p := range selected.Profiles {
		if p.Selected && p.Alias == expectedSelected {
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
	return nil
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
