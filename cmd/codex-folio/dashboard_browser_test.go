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
	"sync/atomic"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/continuation"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/usage"
)

type dashboardClock struct{ control string }

type dashboardProfileAuthenticator struct{ control string }

type dashboardCheckpointHistory struct{ calls *atomic.Int64 }

type dashboardProcessInspector struct{ bootSessionID string }

func (inspector dashboardProcessInspector) BootSessionID() (string, error) {
	return inspector.bootSessionID, nil
}

func (dashboardProcessInspector) IsRunning(int) (bool, error) { return false, nil }

func (history dashboardCheckpointHistory) Candidates(_ context.Context, _ continuation.HistorySource, threadID string) (continuation.CheckpointFields, error) {
	history.calls.Add(1)
	if threadID != "11111111-1111-4111-8111-111111111111" {
		return continuation.CheckpointFields{}, continuation.ErrHistoryUnavailable
	}
	return continuation.CheckpointFields{
		Goal:          continuation.Evidence[string]{Value: "Finish transcript-private-sentinel release"},
		CompletedWork: continuation.Evidence[string]{Value: "Transcript candidate collected"},
		PendingWork:   continuation.Evidence[string]{Value: "Approve a sanitized handoff"},
		Risks:         continuation.Evidence[string]{Value: "transcript-private-sentinel must be removed"},
		NextAction:    continuation.Evidence[string]{Value: "Review the transient candidates"},
	}, nil
}

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
	if suite == "deep" {
		runServiceHealthBrowser(t)
	}
	runOverviewBrowser(t, suite)
}

func runServiceHealthBrowser(t *testing.T) {
	t.Helper()
	for _, scenario := range []struct {
		name   string
		health httpapi.ServiceHealth
	}{
		{name: "locked", health: httpapi.ServiceHealth{ServiceState: httpapi.ServiceStateLocked, VaultState: httpapi.VaultStateLocked, DatabaseState: httpapi.DatabaseStateNotChecked, GuidanceCommands: []string{"codex-folio vault unlock"}}},
		{name: "recovery", health: httpapi.ServiceHealth{ServiceState: httpapi.ServiceStateRecoveryRequired, VaultState: httpapi.VaultStateLocked, DatabaseState: httpapi.DatabaseStateRecoveryRequired, ErrorCode: apperrors.StoreIntegrityFailed, GuidanceCommands: []string{"codex-folio service recovery verify", "codex-folio service recovery list"}}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			lifecycle := &vaultCommandLifecycle{health: scenario.health}
			server, err := httpapi.NewServer(httpapi.Options{CommandToken: "health-browser-command", ServiceLifecycle: lifecycle, StartLocked: true})
			if err != nil {
				t.Fatal(err)
			}
			listener, err := server.Listen()
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			go func() { _ = server.Serve(listener) }()
			output := os.Getenv("CODEX_FOLIO_BROWSER_OUTPUT")
			if output == "" {
				output = t.TempDir()
			} else {
				output = filepath.Join(output, "health-"+scenario.name)
				if err := os.MkdirAll(output, 0700); err != nil {
					t.Fatal(err)
				}
			}
			runner := exec.Command("node", filepath.Join("..", "..", "web", "scripts", "browser-test.mjs"), server.BootstrapURL(), filepath.Join(output, "unused"), "health-"+scenario.name)
			runner.Env = append(os.Environ(), "CODEX_FOLIO_BROWSER_OUTPUT="+output)
			runner.Stdout = os.Stdout
			runner.Stderr = os.Stderr
			if err := runner.Run(); err != nil {
				t.Fatalf("%s service-health browser journey failed: %v", scenario.name, err)
			}
		})
	}
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
	content := "#!/bin/sh\n{ pwd; printf '%s\\n' \"$@\"; } > \"$CODEX_FOLIO_TEST_LAUNCH_LOG\"\nwhile :; do mode=$(cat \"$CODEX_FOLIO_TEST_CONTROL\"); [ \"$mode\" = \"launch-exit-23\" ] && exit 23; [ \"$mode\" = \"handoff-exit-0\" ] && exit 0; sleep 0.01; done\n"
	if runtime.GOOS == "windows" {
		executable += ".cmd"
		content = "@echo off\r\n> \"%CODEX_FOLIO_TEST_LAUNCH_LOG%\" echo %CD%\r\n:args\r\nif \"%~1\"==\"\" goto wait\r\n>> \"%CODEX_FOLIO_TEST_LAUNCH_LOG%\" echo %~1\r\nshift\r\ngoto args\r\n:wait\r\nset \"launch_mode=\"\r\nset /p launch_mode=<\"%CODEX_FOLIO_TEST_CONTROL%\"\r\nif \"%launch_mode%\"==\"launch-exit-23\" exit /b 23\r\nif \"%launch_mode%\"==\"handoff-exit-0\" exit /b 0\r\n>nul ping 127.0.0.1 -n 2\r\ngoto wait\r\n"
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
	configurationPacks, err := newConfigurationPackService(state)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := configurationPacks.CreateDraft(context.Background(), "shared", "1", map[string]string{"config.toml": "model = \"gpt-5\"\n"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurationPacks.Approve(context.Background(), pack.ID, pack.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := configurationPacks.Assign(context.Background(), "Work", pack.ID, pack.Version); err != nil {
		t.Fatal(err)
	}
	if err := configurationPacks.SetOverride(context.Background(), "Work", "config.toml", "model = \"gpt-5-mini\"\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Root, "managed-home", "config.toml"), []byte("model = \"profile-local\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
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
			t.Fatalf("refresh current dashboard fixture for %s: %v", alias, err)
		}
	}
	if err := os.WriteFile(control, []byte("offline"), 0600); err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(filepath.Dir(paths.Root), "atlas")
	if err := os.MkdirAll(repository, 0700); err != nil {
		t.Fatal(err)
	}
	runCheckpointGit(t, repository, "init")
	if err := os.WriteFile(filepath.Join(repository, "handoff-notes.txt"), []byte("browser repository-first context\n"), 0600); err != nil {
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
	activities := seedDashboardActivity(t, state, projects, project, repository)
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
		configurationPacks,
		launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "codex"), Version: "0.153.4"}},
		dashboardProfileAuthenticator{control: control},
	)
	if err != nil {
		t.Fatal(err)
	}
	launches, err := newLaunchCommandService(state, configurationPacks, dashboardProfileAuthenticator{control: control}, projects, service)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := newCheckpointService(state, projects)
	if err != nil {
		t.Fatal(err)
	}
	retainedAt := clock.Now().Add(-48 * time.Hour)
	expiredAt := clock.Now().Add(-time.Hour)
	for _, checkpoint := range []continuation.Checkpoint{
		{
			ID: "checkpoint-browser-retained", Status: continuation.StatusDraft,
			Project: continuation.Project{ID: project.ID, Alias: project.Alias, Basename: project.Basename}, Source: continuation.SourceRepositoryFirst,
			Retention: "30", CreatedAt: retainedAt, Revision: "retained-browser-revision",
		},
		{
			ID: "checkpoint-browser-expired", Status: continuation.StatusExpired,
			Project: continuation.Project{ID: project.ID, Alias: project.Alias, Basename: project.Basename}, Source: continuation.SourceRepositoryFirst,
			Retention: "1", CreatedAt: retainedAt, ExpiresAt: &expiredAt, Revision: "expired-browser-revision",
		},
	} {
		metadata, marshalErr := json.Marshal(checkpoint)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if saveErr := state.SaveCheckpoint(context.Background(), continuation.CheckpointRecord{
			ID: checkpoint.ID, ProjectIdentityID: project.ID, Status: checkpoint.Status,
			Metadata: string(metadata), CreatedAt: checkpoint.CreatedAt, ExpiresAt: checkpoint.ExpiresAt,
		}); saveErr != nil {
			t.Fatal(saveErr)
		}
	}
	launches.continuations = checkpoints
	var historyCalls atomic.Int64
	referencedHome := filepath.Join(filepath.Dir(paths.Root), "dashboard-referenced-home")
	if err := os.MkdirAll(referencedHome, 0700); err != nil {
		t.Fatal(err)
	}
	profileLifecycle, err := newProfileLifecycle(paths, state)
	if err != nil {
		t.Fatal(err)
	}
	server, err := httpapi.NewServer(httpapi.Options{Clock: clock, Usage: service, Selection: selector, Profiles: registry, ProfileLifecycle: profileLifecycle, ProfileAuthentication: profileAuthentication, ConfigurationPacks: configurationPacks, Launches: launches, Projects: projects, Activities: activities, History: usage.NewHistoryService(state), Exports: activity.NewExportService(state), Checkpoints: checkpoints, CheckpointHistory: dashboardCheckpointHistory{calls: &historyCalls}, CollectionSettings: &collectionSettingsCommandService{store: state, enabled: false}, CommandToken: "browser-fixture-command", ServiceEnrollment: func() (string, string, bool) {
		return platform.EnrollmentNotInstalled, "systemd-user", true
	}})
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
	regularLaunchEvidence := make(chan string, 1)
	warmLaunchOverhead := make(chan time.Duration, 1)
	browserUncertainLease := ""
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
			if last == "handoff-source-uncertain" {
				uncertain, prepareErr := state.PrepareLaunch(context.Background(), launch.PrepareRequest{Alias: "Personal", Executable: fakeCodex, WorkingDirectory: repository, ProjectID: project.ID})
				if prepareErr != nil {
					launchErrors <- prepareErr
					return
				}
				browserUncertainLease = uncertain.LeaseID
				continue
			}
			if last == "handoff-source-exit" {
				if browserUncertainLease != "" {
					if abandonErr := state.MarkManagedLaunchAbandoned(context.Background(), browserUncertainLease); abandonErr != nil {
						launchErrors <- abandonErr
						return
					}
				}
				if exitErr := state.MarkManagedLaunchExited(context.Background(), plan.LeaseID, 0); exitErr != nil {
					launchErrors <- exitErr
					return
				}
				continue
			}
			if last == "handoff-restore-running" {
				restored, prepareErr := state.PrepareLaunch(context.Background(), launch.PrepareRequest{Alias: "Work", Executable: fakeCodex, WorkingDirectory: repository, ProjectID: project.ID})
				if prepareErr != nil {
					launchErrors <- prepareErr
					return
				}
				if startErr := state.MarkManagedLaunchStarted(context.Background(), restored.LeaseID, os.Getpid()); startErr != nil {
					launchErrors <- startErr
					return
				}
				continue
			}
			if strings.HasPrefix(last, "handoff-recovery-same:") {
				parts := strings.Split(last, ":")
				if len(parts) != 3 {
					launchErrors <- fmt.Errorf("invalid recovery browser control %q", last)
					return
				}
				sourceLaunch, sourceErr := state.GetManagedLaunch(context.Background(), plan.LeaseID)
				if sourceErr != nil {
					launchErrors <- sourceErr
					return
				}
				original, loadErr := state.LoadCheckpoint(context.Background(), parts[1])
				if loadErr != nil {
					launchErrors <- loadErr
					return
				}
				var recovery continuation.Checkpoint
				if unmarshalErr := json.Unmarshal([]byte(original.Metadata), &recovery); unmarshalErr != nil {
					launchErrors <- unmarshalErr
					return
				}
				recovery.ID = parts[1] + "-recovery"
				metadata, marshalErr := json.Marshal(recovery)
				if marshalErr != nil {
					launchErrors <- marshalErr
					return
				}
				if saveErr := state.SaveCheckpoint(context.Background(), continuation.CheckpointRecord{
					ID: recovery.ID, ProjectIdentityID: original.ProjectIdentityID, Status: continuation.StatusApproved,
					Metadata: string(metadata), CreatedAt: original.CreatedAt, ExpiresAt: original.ExpiresAt,
				}); saveErr != nil {
					launchErrors <- saveErr
					return
				}
				_, prepareErr := state.PrepareLaunch(context.Background(), launch.PrepareRequest{
					Alias: "Personal", Executable: fakeCodex, WorkingDirectory: repository,
					ProjectID: project.ID, CheckpointID: recovery.ID, CheckpointRevision: parts[2],
					SourceProfileID: sourceLaunch.ProfileID, BootSessionID: "boot-a",
				})
				if prepareErr != nil {
					launchErrors <- prepareErr
					return
				}
				if reconcileErr := state.ReconcileManagedLaunches(context.Background(), dashboardProcessInspector{bootSessionID: "boot-a"}); reconcileErr != nil {
					launchErrors <- reconcileErr
					return
				}
				continue
			}
			if last == "handoff-recovery-changed" {
				if reconcileErr := state.ReconcileManagedLaunches(context.Background(), dashboardProcessInspector{bootSessionID: "boot-b"}); reconcileErr != nil {
					launchErrors <- reconcileErr
					return
				}
				continue
			}
			if last != "launch-run-23" && last != "launch-fail" && !strings.HasPrefix(last, "handoff-run-0:") {
				continue
			}
			if strings.HasPrefix(last, "handoff-run-0:") {
				parts := strings.Split(last, ":")
				if len(parts) != 3 {
					launchErrors <- fmt.Errorf("invalid handoff browser control %q", last)
					return
				}
				var handoffStderr bytes.Buffer
				code := runHandoffWithDependencies(
					[]string{"Personal", "--checkpoint", parts[1], "--revision", parts[2], "--codex-bin", fakeCodex}, strings.NewReader(""), io.Discard, &handoffStderr,
					func(*string) (platform.Paths, error) { return paths, nil }, launchTestResolver{candidate: launch.Candidate{Path: fakeCodex, Version: "0.153.4"}}, nil, newForegroundProcess, nil, nil,
					func() profile.Authenticator { return dashboardProfileAuthenticator{control: control} }, platform.OwnerOptions{},
				)
				if code != 0 {
					checkpoint, checkpointErr := state.LoadCheckpoint(context.Background(), parts[1])
					source, sourceErr := state.LatestSourceLaunch(context.Background(), project.ID)
					launchErrors <- fmt.Errorf("unexpected browser fixture handoff result %d: %s; checkpoint=%#v (%v); source=%#v (%v)", code, handoffStderr.String(), checkpoint, checkpointErr, source, sourceErr)
				}
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
			if last == "launch-run-23" {
				content, readErr := os.ReadFile(launchLog)
				if readErr != nil {
					launchErrors <- readErr
					return
				}
				regularLaunchEvidence <- string(content)
			}
		}
	}()
	analyticsSeedErrors := make(chan error, 1)
	if suite == "deep" {
		go func() {
			seeded := false
			activitySeeded := false
			for {
				select {
				case <-stopLaunches:
					return
				case <-time.After(10 * time.Millisecond):
				}
				mode, readErr := os.ReadFile(control)
				if readErr != nil {
					continue
				}
				if string(mode) == "analytics-seed" && !seeded {
					if seedErr := seedDashboardAnalyticsHistory(state); seedErr != nil {
						analyticsSeedErrors <- seedErr
						_ = os.WriteFile(control, []byte("analytics-seed-failed"), 0600)
						return
					}
					if writeErr := os.WriteFile(control, []byte("analytics-seeded"), 0600); writeErr != nil {
						analyticsSeedErrors <- writeErr
						return
					}
					seeded = true
				}
				if string(mode) == "analytics-activity-seed" && seeded && !activitySeeded {
					_, seedErr := state.SetAnalyticsRetention(context.Background(), "90")
					if seedErr == nil {
						_, seedErr = activities.Refresh(context.Background(), "Personal")
					}
					if seedErr != nil {
						analyticsSeedErrors <- seedErr
						_ = os.WriteFile(control, []byte("analytics-activity-seed-failed"), 0600)
						return
					}
					if writeErr := os.WriteFile(control, []byte("analytics-activity-seeded"), 0600); writeErr != nil {
						analyticsSeedErrors <- writeErr
						return
					}
					activitySeeded = true
				}
				if string(mode) == "analytics-clean" && seeded {
					scope := usage.HistoryScope{ProfileID: "*", ProjectID: "*", From: "all", To: "all", Classes: []string{"aggregates"}}
					preview, purgeErr := state.PurgeAnalytics(context.Background(), scope, "")
					if purgeErr == nil {
						_, purgeErr = state.PurgeAnalytics(context.Background(), scope, preview.Confirmation)
					}
					if purgeErr != nil {
						analyticsSeedErrors <- purgeErr
						_ = os.WriteFile(control, []byte("analytics-clean-failed"), 0600)
						return
					}
					if writeErr := os.WriteFile(control, []byte("analytics-cleaned"), 0600); writeErr != nil {
						analyticsSeedErrors <- writeErr
					}
					return
				}
			}
		}()
	}
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
	if content, err := os.ReadFile(referencedMarker); err != nil || string(content) != "externally owned" {
		t.Fatalf("referenced Identity Home marker changed: %q, %v", content, err)
	}
	select {
	case launchErr := <-launchErrors:
		t.Fatal("browser launch fixture failed:", launchErr)
	default:
	}
	if runnerErr != nil {
		t.Fatal("Overview browser journey failed:", runnerErr)
	}
	select {
	case seedErr := <-analyticsSeedErrors:
		t.Fatal("browser analytics fixture failed:", seedErr)
	default:
	}
	if suite == "deep" {
		if historyCalls.Load() != 3 {
			t.Fatalf("transcript history calls = %d, want one unavailable and two successful authorized reviews", historyCalls.Load())
		}
		content := <-regularLaunchEvidence
		lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n")), "\n")
		if len(lines) != 3 || filepath.Clean(lines[0]) != filepath.Clean(repository) || !slices.Equal(lines[1:], []string{"--model", "gpt-5"}) {
			t.Fatalf("native fake Codex launch = %q, want working directory %q and transported arguments", content, repository)
		}
		handoffContent, err := os.ReadFile(launchLog)
		if err != nil {
			t.Fatal(err)
		}
		handoffLines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(handoffContent), "\r\n", "\n")), "\n")
		if len(handoffLines) != 2 || filepath.Clean(handoffLines[0]) != filepath.Clean(repository) || !strings.Contains(handoffLines[1], `"source":"transcript-assisted"`) || strings.Contains(handoffLines[1], "transcript-private-sentinel") {
			t.Fatalf("native fake Codex handoff = %q, want sanitized transcript-assisted context in %q", handoffContent, repository)
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
	if suite == "deep" {
		if err != nil || record.ProfileAlias != "Work" || record.State != launch.StateExited {
			t.Fatal("Safe Continuation did not preserve the original Work launch's definitive exit")
		}
		workActivity, listErr := state.ListActivity(context.Background(), activity.Filters{ProfileAlias: "Work", ProjectID: project.ID})
		if listErr != nil || !slices.ContainsFunc(workActivity, func(item activity.TimelineRecord) bool {
			return item.ProfileAlias == "Work" && item.Lifecycle == string(launch.StateRunning)
		}) {
			t.Fatal("browser selection changed the restored running Work launch")
		}
	} else if err != nil || record.ProfileAlias != "Work" || record.State != launch.StateRunning {
		t.Fatal("browser selection changed the running Work launch")
	}
	return nil
}

func seedDashboardAnalyticsHistory(state *store.Store) error {
	ctx := context.Background()
	metrics := usage.Registry()[:2]
	for _, alias := range []string{"Work", "Personal"} {
		target, err := state.ResolveUsageProfile(ctx, alias)
		if err != nil {
			return fmt.Errorf("resolve analytics fixture profile %s: %w", alias, err)
		}
		target.LoginIdentity = "analytics-shared-login@example.test"
		target.Workspace = "analytics-private-workspace"
		for monthIndex := 0; monthIndex < 13; monthIndex++ {
			if monthIndex == 3 {
				continue
			}
			captured := time.Date(2025, time.August+time.Month(monthIndex), 1, 23, 30, 0, 0, time.UTC)
			snapshot := usage.NewUnavailableSnapshot("0.153.4", captured, usage.AvailabilityUnsupported, usage.ReasonUnsupported)
			snapshot.Status = usage.AvailabilityPartial
			snapshot.TriggerReason = usage.TriggerExplicitRefresh
			for metricIndex, metric := range metrics {
				start := captured.Add(-time.Duration([]int{300, 10080}[metricIndex]) * time.Minute)
				end := captured.Add(time.Duration([]int{300, 10080}[metricIndex]) * time.Minute)
				provenance := usage.ProvenanceProvider
				source, sourceVersion := usage.SourceCodexAppServer, "0.153.4"
				freshness, availability := usage.FreshnessFresh, usage.AvailabilityAvailable
				assumptions, uncertainty := "", ""
				if monthIndex == 1 && metricIndex == 1 {
					provenance, source, sourceVersion = usage.ProvenanceLocal, usage.SourceDerived, "derived-v1"
				}
				if monthIndex == 2 && metricIndex == 0 {
					provenance, source, sourceVersion = usage.ProvenanceEstimated, usage.SourceDerived, "estimate-v1"
					assumptions, uncertainty = "bounded fixture estimate", "not provider reported"
				}
				if monthIndex == 4 && metricIndex == 0 {
					freshness, availability = usage.FreshnessStale, usage.AvailabilityStale
				}
				if monthIndex == 5 && metricIndex == 1 {
					availability = usage.AvailabilityContradictory
				}
				observation := usage.Observation{
					Metric: metric, Value: float64(18 + monthIndex*2 + metricIndex*7),
					ObservedAt: captured, CapturedAt: captured, WindowStart: &start, WindowEnd: &end,
					WindowTimezone: "UTC", Source: source, SourceVersion: sourceVersion,
					Provenance: provenance, Freshness: freshness, Availability: availability,
					Assumptions: assumptions, Uncertainty: uncertainty,
				}
				snapshot.Observations = append(snapshot.Observations, observation)
				if monthIndex == 5 && metricIndex == 1 {
					conflict := observation
					conflict.Value = 77
					conflict.Source = usage.SourceDerived
					conflict.SourceVersion = "conflict-v1"
					conflict.Provenance = usage.ProvenanceLocal
					snapshot.Observations = append(snapshot.Observations, conflict)
				}
				snapshot.Availability[metricIndex].State = availability
				snapshot.Availability[metricIndex].Reason = ""
				snapshot.Availability[metricIndex].Provenance = provenance
			}
			if _, err := state.SaveUsageSnapshot(ctx, target, snapshot); err != nil {
				return fmt.Errorf("save analytics fixture profile %s month %d: %w", alias, monthIndex, err)
			}
		}
	}
	if _, err := state.SetAnalyticsRetention(ctx, "30"); err != nil {
		return fmt.Errorf("set analytics fixture retention: %w", err)
	}
	result, err := state.RetainAnalytics(ctx)
	if err != nil {
		return fmt.Errorf("compact analytics fixture: %w", err)
	}
	if result.More {
		return fmt.Errorf("compact analytics fixture left more work: %#v", result)
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
