package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/telemetry"
)

func TestTelemetryStateDefaultsOffAndPersistsExplicitConsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite3")
	stateStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := stateStore.TelemetryState(context.Background())
	if err != nil || initial.Consent.Enabled || initial.InstallationID != "" {
		t.Fatalf("initial state = %+v, err = %v", initial, err)
	}
	want := telemetry.State{
		Consent:        telemetry.Consent{Enabled: true, SchemaVersion: telemetry.SchemaVersion, ConsentedAt: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)},
		InstallationID: "123e4567e89b42d3a456426614174000",
	}
	if _, err := stateStore.SetTelemetryState(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.TelemetryState(context.Background())
	if err != nil || got != want {
		t.Fatalf("reopened state = %+v, want %+v, err = %v", got, want, err)
	}
}

func TestTelemetryStateRejectsImplicitConsent(t *testing.T) {
	stateStore, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	_, err = stateStore.SetTelemetryState(context.Background(), telemetry.State{Consent: telemetry.Consent{Enabled: true}})
	if err == nil {
		t.Fatal("SetTelemetryState accepted consent without an exact schema version and timestamp")
	}
}
