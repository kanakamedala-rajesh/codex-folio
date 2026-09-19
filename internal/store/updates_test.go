package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/updates"
)

func TestUpdateSettingsDefaultOffAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex-folio.sqlite3")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.UpdateSettings(context.Background())
	if err != nil || settings.AutomaticChecks {
		t.Fatalf("default settings = %#v, %v", settings, err)
	}
	if _, err := state.SetUpdateSettings(context.Background(), updates.Settings{AutomaticChecks: true}); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	settings, err = reopened.UpdateSettings(context.Background())
	if err != nil || !settings.AutomaticChecks {
		t.Fatalf("reopened settings = %#v, %v", settings, err)
	}
	settings, err = reopened.SetUpdateSettings(context.Background(), updates.Settings{})
	if err != nil || settings.AutomaticChecks {
		t.Fatalf("revoked settings = %#v, %v", settings, err)
	}
}

func TestUpdateCheckStatePersistsOnlyValidatedPublicProjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex-folio.sqlite3")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 20, 12, 0, 0, 123, time.UTC)
	want := updates.CheckState{
		Status: updates.StatusUpdateAvailable, CurrentVersion: "1.0.0", AvailableVersion: "1.1.0",
		ReleaseNotes: "Public notes", DownloadURL: "https://updates.example/releases/1.1.0.zip",
		InstallerGuidance: "Stop the service and verify the signature.", CheckedAt: now, NextCheckAt: now.Add(updates.NormalCheckInterval),
	}
	if _, err := state.SetUpdateCheckState(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.UpdateCheckState(context.Background())
	if err != nil || got != want {
		t.Fatalf("UpdateCheckState() = %#v, %v; want %#v", got, err, want)
	}

	invalid := want
	invalid.ReleaseNotes = strings.Repeat("x", updates.MaxReleaseNotesLength+1)
	if _, err := reopened.SetUpdateCheckState(context.Background(), invalid); !errors.Is(err, updates.ErrInvalid) {
		t.Fatalf("oversize SetUpdateCheckState() = %v", err)
	}
	if _, err := reopened.db.Exec(`UPDATE update_check_state SET release_notes = ? WHERE update_check_state_id = 1`, invalid.ReleaseNotes); err == nil {
		t.Fatal("SQLite accepted oversized release notes")
	}
	if _, err := reopened.db.Exec(`UPDATE update_check_state SET error_code = 'PRIVATE_CAUSE' WHERE update_check_state_id = 1`); err == nil {
		t.Fatal("SQLite accepted unbounded error code")
	}
}

func TestUpdateCheckStateDefaultsToNeverChecked(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "codex-folio.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	got, err := state.UpdateCheckState(context.Background())
	if err != nil || got.Status != updates.StatusNeverChecked || got.CurrentVersion != "" || !got.CheckedAt.IsZero() {
		t.Fatalf("default state = %#v, %v", got, err)
	}
}

func TestUpdateMigrationPreservesV22SettingsAndDefaultsOptOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex-folio.sqlite3")
	injected := errors.New("stop before update migration")
	state, err := OpenWithOptions(Options{Path: path, MigrationHook: MigrationHooks{Before: func(version int) error {
		if version == 23 {
			return injected
		}
		return nil
	}}})
	if state != nil {
		state.Close()
	}
	if !errors.Is(err, injected) {
		t.Fatalf("OpenWithOptions() = %v", err)
	}

	migrated, err := Open(path)
	if err != nil {
		t.Fatalf("Open(v22) = %v", err)
	}
	defer migrated.Close()
	settings, err := migrated.UpdateSettings(context.Background())
	if err != nil || settings.AutomaticChecks {
		t.Fatalf("migrated settings = %#v, %v", settings, err)
	}
	diagnostics, err := migrated.DiagnosticSettings(context.Background())
	if err != nil || diagnostics.RetentionDays != 14 || !diagnostics.Enabled {
		t.Fatalf("v22 diagnostics changed = %#v, %v", diagnostics, err)
	}
}
