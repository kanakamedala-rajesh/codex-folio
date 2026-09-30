package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/usage"
	"venkatasudha.com/codex-folio/internal/vault"
)

type consentJourneyScheduleStore struct {
	state       *store.Store
	targetReads int
}

func (fixture *consentJourneyScheduleStore) CollectionSettings(ctx context.Context) (usage.CollectionSettings, error) {
	return fixture.state.CollectionSettings(ctx)
}

func (fixture *consentJourneyScheduleStore) CollectionScheduleTargets(context.Context) ([]usage.ScheduleTarget, error) {
	fixture.targetReads++
	return []usage.ScheduleTarget{{Profile: usage.ProfileTarget{ID: "work", Alias: "Work"}, Active: true}}, nil
}

func (fixture *consentJourneyScheduleStore) SaveCollectionScheduleState(context.Context, string, usage.ScheduleState) error {
	return nil
}

type consentJourneySource struct{ calls int }

type collectionConsentDoerFunc func(*http.Request) (*http.Response, error)

func (do collectionConsentDoerFunc) Do(request *http.Request) (*http.Response, error) {
	return do(request)
}

func TestCollectionConsentServiceFailuresDoNotBlockForeground(t *testing.T) {
	for _, test := range []struct {
		name       string
		failMethod string
		wantPuts   int
	}{
		{name: "settings read", failMethod: http.MethodGet},
		{name: "consent save", failMethod: http.MethodPut, wantPuts: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			puts := 0
			client := httpapi.NewCommandClient("http://localhost", "token", collectionConsentDoerFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodPut {
					puts++
				}
				if request.Method == test.failMethod {
					return nil, errors.New("optional collection service unavailable")
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"active_interval_seconds":300,"idle_interval_seconds":1800,"consent":"undecided"}`))}, nil
			}))
			var output, diagnostics bytes.Buffer
			if code := offerCollectionConsentWithClient(client, strings.NewReader("y\n"), &output, &diagnostics, true); code != exitSuccess {
				t.Fatalf("optional collection failure stopped foreground: code=%d diagnostics=%q", code, diagnostics.String())
			}
			if puts != test.wantPuts || !strings.Contains(diagnostics.String(), "Background collection setup could not complete") || strings.Contains(output.String(), "Background collection accepted") {
				t.Fatalf("puts=%d output=%q diagnostics=%q", puts, output.String(), diagnostics.String())
			}
		})
	}
}

func (source *consentJourneySource) Refresh(context.Context, string, string) (usage.Snapshot, error) {
	source.calls++
	return usage.Snapshot{}, nil
}

func TestCollectionConsentRealServiceRestartAndSourceBoundary(t *testing.T) {
	root := t.TempDir()
	override := filepath.Join(root, "state")
	paths, err := platform.ResolvePaths(platform.PathOptions{Platform: platform.Platform(runtime.GOOS), HomeDir: filepath.Join(root, "home"), OwnerHomeDir: filepath.Join(root, "home"), StateRootOverride: &override, Environment: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	path := paths.DatabaseFile
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	secureVault, err := vault.NewMemoryVault(vault.MemoryVaultOptions{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.OpenWithVault(path, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	services, err := composeServiceOperationalServices(paths, state, false)
	if err != nil {
		t.Fatal(err)
	}
	server, err := httpapi.NewServer(serviceServerOptions(nil, "collection-consent-fixture", services))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	client := httpapi.NewCommandClient(server.Origin(), "collection-consent-fixture", nil)
	clock := fixedConsentClock{at: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)}
	fixture := &consentJourneyScheduleStore{state: state}
	source := &consentJourneySource{}
	gate := services.CollectionSettings.(*collectionSettingsCommandService).gate
	scheduler, err := usage.NewScheduler(collectionConsentStore{ScheduleStore: fixture, consentReader: state}, collectionConsentRefresher{reader: state, refresher: source, gate: gate}, clock, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	current, err := client.CollectionSettings(ctx)
	if err != nil || current.Consent != "undecided" || current.SchedulerEnabled {
		t.Fatalf("initial service settings = %#v, %v", current, err)
	}
	if _, err := scheduler.Tick(ctx); err != nil || fixture.targetReads != 0 || source.calls != 0 {
		t.Fatalf("unanswered tick reached source: reads=%d calls=%d err=%v", fixture.targetReads, source.calls, err)
	}
	var prompt, promptErrors bytes.Buffer
	if code := offerCollectionConsentWithClient(client, strings.NewReader("y\n"), &prompt, &promptErrors, false); code != exitSuccess || prompt.Len() != 0 {
		t.Fatalf("non-interactive start granted consent or prompted: code=%d output=%q errors=%q", code, prompt.String(), promptErrors.String())
	}
	prompt.Reset()
	if code := offerCollectionConsentWithClient(client, strings.NewReader("y\n"), &prompt, &promptErrors, true); code != exitSuccess || !strings.Contains(prompt.String(), "Optional background collection") {
		t.Fatalf("guided acceptance code=%d output=%q errors=%q", code, prompt.String(), promptErrors.String())
	}
	accepted := "accepted"
	current, err = client.CollectionSettings(ctx)
	if err != nil || current.Consent != accepted || !current.SchedulerEnabled {
		t.Fatalf("accepted service settings = %#v, %v", current, err)
	}
	prompt.Reset()
	if code := offerCollectionConsentWithClient(client, strings.NewReader("n\n"), &prompt, &promptErrors, true); code != exitSuccess || prompt.Len() != 0 {
		t.Fatalf("remembered choice prompted again: code=%d output=%q errors=%q", code, prompt.String(), promptErrors.String())
	}
	if result, err := scheduler.Tick(ctx); err != nil || result.Collected != 1 || source.calls != 1 {
		t.Fatalf("accepted tick = %#v, calls=%d, error=%v", result, source.calls, err)
	}
	declined := "declined"
	current, err = client.SetCollectionSettings(ctx, httpapi.CollectionSettingsRequest{ActiveIntervalSeconds: 300, IdleIntervalSeconds: 1800, Consent: &declined})
	if err != nil || current.Consent != declined || current.SchedulerEnabled {
		t.Fatalf("declined service settings = %#v, %v", current, err)
	}
	if _, err := scheduler.Tick(ctx); err != nil || source.calls != 1 || fixture.targetReads != 1 {
		t.Fatalf("declined tick reached source: reads=%d calls=%d err=%v", fixture.targetReads, source.calls, err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := services.Background.Close(); err != nil {
		t.Fatal(err)
	}
	if err := services.Shutdown.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = store.OpenWithVault(path, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	restartedServices, err := composeServiceOperationalServices(paths, state, false)
	if err != nil {
		t.Fatal(err)
	}
	defer restartedServices.Background.Close()
	defer restartedServices.Shutdown.Close()
	restartedServer, err := httpapi.NewServer(serviceServerOptions(nil, "restarted-collection-consent-fixture", restartedServices))
	if err != nil {
		t.Fatal(err)
	}
	restartedListener, err := restartedServer.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer restartedServer.Close()
	go func() { _ = restartedServer.Serve(restartedListener) }()
	restartedClient := httpapi.NewCommandClient(restartedServer.Origin(), "restarted-collection-consent-fixture", nil)
	restartedSettings, err := restartedClient.CollectionSettings(ctx)
	if err != nil || restartedSettings.Consent != "declined" || restartedSettings.SchedulerEnabled {
		t.Fatalf("restarted service settings = %#v, %v", restartedSettings, err)
	}
	if choice, err := state.CollectionConsent(ctx); err != nil || choice != usage.CollectionConsentDeclined {
		t.Fatalf("restarted consent = %s, %v", choice, err)
	}
}
