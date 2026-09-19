package telemetryadapter

import (
	"context"
	"errors"
	"testing"

	"venkatasudha.com/codex-folio/internal/telemetry"
)

func adapterEvent() telemetry.EventV1 {
	return telemetry.EventV1{
		SchemaVersion: telemetry.SchemaVersion, AppVersion: "1.2.3", OSFamily: telemetry.OSLinux,
		Architecture: telemetry.ArchitectureAMD64, Feature: telemetry.FeatureDashboard,
		Outcome: telemetry.OutcomeSucceeded, DurationBucket: telemetry.DurationUnderOneSecond,
		InstallationID: "00112233445566778899aabbccddeeff",
	}
}

func TestDisabledAdaptersHaveNoRemoteCapability(t *testing.T) {
	disabled := DisabledTransport{}
	if err := disabled.Send(context.Background(), adapterEvent()); !errors.Is(err, telemetry.ErrTransportDisabled) {
		t.Fatalf("disabled transport error = %v", err)
	}
	if err := disabled.DeleteInstallation(context.Background(), "00112233445566778899aabbccddeeff"); !errors.Is(err, telemetry.ErrTransportDisabled) {
		t.Fatalf("disabled deletion error = %v", err)
	}
	prerequisites, err := (DisabledPrerequisites{}).TelemetryPrerequisites(context.Background())
	if err != nil || prerequisites.Ready() {
		t.Fatalf("disabled prerequisites = %+v, %v", prerequisites, err)
	}
}

func TestRecordingTransportCopiesEventsAndReturnsConfiguredFailure(t *testing.T) {
	wantErr := errors.New("offline")
	transport := NewRecordingTransport(wantErr)
	if err := transport.Send(context.Background(), adapterEvent()); !errors.Is(err, wantErr) {
		t.Fatalf("recording error = %v", err)
	}
	if err := transport.DeleteInstallation(context.Background(), "00112233445566778899aabbccddeeff"); !errors.Is(err, wantErr) {
		t.Fatalf("recording deletion error = %v", err)
	}
	events := transport.Events()
	if len(events) != 1 || events[0].Feature != telemetry.FeatureDashboard {
		t.Fatalf("recorded events = %+v", events)
	}
	events[0].Feature = telemetry.FeatureUpdates
	if transport.Events()[0].Feature != telemetry.FeatureDashboard {
		t.Fatal("Events returned mutable adapter storage")
	}
	deletions := transport.Deletions()
	if len(deletions) != 1 || deletions[0] != "00112233445566778899aabbccddeeff" {
		t.Fatalf("recorded deletions = %v", deletions)
	}
	deletions[0] = "changed"
	if transport.Deletions()[0] != "00112233445566778899aabbccddeeff" {
		t.Fatal("Deletions returned mutable adapter storage")
	}
}

func TestRandomIDGeneratorProducesValidEventIDs(t *testing.T) {
	first, err := (RandomIDGenerator{}).NewInstallationID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := (RandomIDGenerator{}).NewInstallationID()
	if err != nil {
		t.Fatal(err)
	}
	event := adapterEvent()
	event.InstallationID = first
	if event.Validate() != nil || first == second {
		t.Fatalf("random IDs are invalid or repeated: %q %q", first, second)
	}
}
