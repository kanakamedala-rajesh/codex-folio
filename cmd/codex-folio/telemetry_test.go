package main

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	telemetryadapter "venkatasudha.com/codex-folio/internal/adapters/telemetry"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/buildinfo"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/telemetry"
)

type cliTelemetryPrerequisites struct{}

func (cliTelemetryPrerequisites) TelemetryPrerequisites(context.Context) (telemetry.Prerequisites, error) {
	return telemetry.Prerequisites{Endpoint: true, PublicSchema: true, PrivacyNotice: true, EventRetention: true, AggregateRetention: true, Deletion: true, Reset: true}, nil
}

type cliBlockingRecordingTransport struct {
	recording *telemetryadapter.RecordingTransport
	started   chan struct{}
	finished  chan struct{}
	once      sync.Once
}

func (transport *cliBlockingRecordingTransport) Send(ctx context.Context, event telemetry.EventV1) error {
	_ = transport.recording.Send(ctx, event)
	transport.once.Do(func() { close(transport.started) })
	<-ctx.Done()
	close(transport.finished)
	return ctx.Err()
}

func (transport *cliBlockingRecordingTransport) DeleteInstallation(ctx context.Context, installationID string) error {
	return transport.recording.DeleteInstallation(ctx, installationID)
}

func TestTelemetryCLIUsesRunningAuthenticatedServiceWithRecordingAdapter(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.OpenWithVault(paths.DatabaseFile, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	recording := telemetryadapter.NewRecordingTransport(nil)
	transport := &cliBlockingRecordingTransport{recording: recording, started: make(chan struct{}), finished: make(chan struct{})}
	service, err := telemetry.NewService(context.Background(), telemetry.ServiceOptions{
		Repository: state, Prerequisites: cliTelemetryPrerequisites{}, Transport: transport,
		Clock: usageClock{}, IDGenerator: telemetryadapter.RandomIDGenerator{}, AppVersion: buildinfo.Version,
		OSFamily: telemetryOSFamily(), Architecture: telemetry.ArchitectureAMD64, AttemptTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	const token = "telemetry-running-owner-token"
	server, err := httpapi.NewServer(httpapi.Options{Telemetry: service, CommandToken: token})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: token}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = service.Close(context.Background())
		_ = state.Close()
		_ = owner.Close()
	})

	run := func(arguments ...string) httpapi.TelemetryResponse {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := runTelemetry(append(arguments, "--json"), &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil })
		if code != exitSuccess || stderr.Len() != 0 {
			t.Fatalf("runTelemetry(%v) = %d, stdout=%q stderr=%q", arguments, code, stdout.String(), stderr.String())
		}
		var response httpapi.TelemetryResponse
		if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	if service.Record(telemetry.Record{Feature: telemetry.FeatureDashboard, Outcome: telemetry.OutcomeSucceeded, DurationBucket: telemetry.DurationUnderOneSecond}) {
		t.Fatal("pre-consent record was accepted")
	}
	if initial := run("status"); initial.Status != "disabled" || initial.Enabled || len(recording.Events()) != 0 {
		t.Fatalf("initial = %#v events=%v", initial, recording.Events())
	}
	if enabled := run("enable", "--schema-version", "1"); !enabled.Enabled || !enabled.InstallationIdPresent {
		t.Fatalf("enabled = %#v", enabled)
	}
	recordStarted := time.Now()
	if !service.Record(telemetry.Record{Feature: telemetry.FeatureDashboard, Outcome: telemetry.OutcomeFailed, ErrorCode: apperrors.CLIInternal, DurationBucket: telemetry.DurationUnderOneSecond}) {
		t.Fatal("post-consent record was not accepted")
	}
	if elapsed := time.Since(recordStarted); elapsed >= 40*time.Millisecond {
		t.Fatalf("Record blocked on transport for %v", elapsed)
	}
	select {
	case <-transport.started:
	case <-time.After(time.Second):
		t.Fatal("recording transport did not receive the event")
	}
	events := recording.Events()
	if len(events) != 1 || events[0].SchemaVersion != telemetry.SchemaVersion || events[0].Feature != telemetry.FeatureDashboard ||
		events[0].Outcome != telemetry.OutcomeFailed || events[0].ErrorCode != apperrors.CLIInternal ||
		events[0].DurationBucket != telemetry.DurationUnderOneSecond || events[0].InstallationID == "" {
		t.Fatalf("delivered event is not the valid allowlisted event: %#v", events)
	}
	select {
	case <-transport.finished:
	case <-time.After(time.Second):
		t.Fatal("blocking transport attempt exceeded its configured timeout")
	}
	if reset := run("reset-id"); !reset.InstallationIdPresent {
		t.Fatalf("reset = %#v", reset)
	}
	if revoked := run("revoke"); revoked.Enabled || revoked.Status != "disabled" {
		t.Fatalf("revoked = %#v", revoked)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := service.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	if len(recording.Events()) != 1 || len(recording.Deletions()) != 2 {
		t.Fatalf("recording adapter events=%v deletions=%v", recording.Events(), recording.Deletions())
	}
}

func TestTelemetryEnableRequiresExplicitSchemaVersion(t *testing.T) {
	if _, err := parseTelemetryOptions([]string{"enable"}); err == nil {
		t.Fatal("enable accepted implicit consent")
	}
	options, err := parseTelemetryOptions([]string{"enable", "--schema-version", "1", "--json"})
	if err != nil || options.schemaVersion == nil || *options.schemaVersion != 1 || !options.json {
		t.Fatalf("options = %+v, err = %v", options, err)
	}
}

func TestTelemetryOtherActionsRejectConsentFlag(t *testing.T) {
	if _, err := parseTelemetryOptions([]string{"status", "--schema-version", "1"}); err == nil {
		t.Fatal("status accepted a consent flag")
	}
}
