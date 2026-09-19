package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
)

func TestDiagnosticAggregatesPersistOnlyRedactedBoundedBuckets(t *testing.T) {
	databasePath := t.TempDir() + "/codex-folio.sqlite3"
	clock := fixedStoreClock{now: time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)}
	foundation, err := OpenWithOptions(Options{Path: databasePath, Clock: clock})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = foundation.Close() }()

	at := clock.now
	event, err := diagnostics.NewEvent(at, diagnostics.SeverityError, diagnostics.ComponentStore, apperrors.StoreIntegrityFailed, diagnostics.Context{
		Operation:     diagnostics.OperationIntegrityCheck,
		State:         diagnostics.StateFailed,
		SchemaVersion: 1,
	})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if err := foundation.RecordDiagnostic(context.Background(), event); err != nil {
		t.Fatalf("RecordDiagnostic(first) error = %v", err)
	}
	if err := foundation.RecordDiagnostic(context.Background(), event); err != nil {
		t.Fatalf("RecordDiagnostic(second) error = %v", err)
	}

	aggregates, err := foundation.ListDiagnosticAggregates(context.Background())
	if err != nil {
		t.Fatalf("ListDiagnosticAggregates() error = %v", err)
	}
	if len(aggregates) != 1 {
		t.Fatalf("aggregates = %#v, want one bucket", aggregates)
	}
	if aggregates[0].OccurrenceCount != 2 || aggregates[0].ErrorCode != apperrors.StoreIntegrityFailed || aggregates[0].Component != diagnostics.ComponentStore {
		t.Fatalf("aggregate = %#v, want redacted repeated store failure bucket", aggregates[0])
	}
	encoded, err := json.Marshal(aggregates)
	if err != nil {
		t.Fatalf("json.Marshal(aggregates) error = %v", err)
	}
	for _, forbidden := range []string{"context", "message", "canonical_path", "repository", "cookie", "bootstrap", "ciphertext"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("aggregates %q contain forbidden value/field %q", encoded, forbidden)
		}
	}

	if err := foundation.PurgeDiagnosticAggregates(context.Background(), at.Add(time.Second)); err != nil {
		t.Fatalf("PurgeDiagnosticAggregates() error = %v", err)
	}
	aggregates, err = foundation.ListDiagnosticAggregates(context.Background())
	if err != nil {
		t.Fatalf("ListDiagnosticAggregates(after purge) error = %v", err)
	}
	if len(aggregates) != 0 {
		t.Fatalf("aggregates after purge = %#v, want empty", aggregates)
	}
}

func TestDiagnosticAggregatesHaveAStableMaximumRowCount(t *testing.T) {
	clock := fixedStoreClock{now: time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)}
	foundation, err := OpenWithOptions(Options{Path: t.TempDir() + "/codex-folio.sqlite3", Clock: clock})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = foundation.Close() }()

	codeSet := []string{
		apperrors.CLIUsage,
		apperrors.CLIInternal,
		apperrors.PlatformStatePathInvalid,
		apperrors.PlatformStatePathUnsafe,
		apperrors.PlatformPermissionDenied,
		apperrors.PlatformServiceAlreadyRunning,
		apperrors.PlatformServiceMetadataInvalid,
		apperrors.PlatformServiceUnavailable,
		apperrors.HTTPAPIHostInvalid,
		apperrors.HTTPAPIOriginInvalid,
		apperrors.HTTPAPIBootstrapInvalid,
		apperrors.HTTPAPISessionInvalid,
		apperrors.HTTPAPISessionExpired,
		apperrors.HTTPAPICSRFInvalid,
		apperrors.HTTPAPIMethodNotAllowed,
		apperrors.HTTPAPIRouteNotFound,
		apperrors.HTTPAPIServiceUnavailable,
		apperrors.StoreOpenFailed,
		apperrors.StoreIntegrityFailed,
		apperrors.StoreSchemaIncompatible,
		apperrors.StoreMigrationFailed,
		apperrors.StoreMigrationPartial,
		apperrors.StoreReadFailed,
		apperrors.StoreWriteFailed,
		apperrors.StoreDiagnosticWriteFailed,
		apperrors.StoreBackupFailed,
		apperrors.StoreRecoveryCandidateInvalid,
		apperrors.StoreRecoveryCandidateNotFound,
		apperrors.StoreRecoveryRestoreFailed,
		apperrors.VaultUnavailable,
		apperrors.VaultLocked,
		apperrors.VaultKeyInvalid,
		apperrors.VaultEnvelopeInvalid,
		apperrors.VaultEnvelopeUnsupported,
		apperrors.VaultKeyGenerationMismatch,
		apperrors.VaultEncryptionFailed,
	}
	at := clock.now
	components := []string{diagnostics.ComponentCLI, diagnostics.ComponentHTTPAPI, diagnostics.ComponentPlatform, diagnostics.ComponentStore, diagnostics.ComponentVault, diagnostics.ComponentDiagnostics}
	severities := []diagnostics.Severity{diagnostics.SeverityInfo, diagnostics.SeverityWarning, diagnostics.SeverityError}
	for _, component := range components {
		for _, code := range codeSet {
			for _, severity := range severities {
				event, eventErr := diagnostics.NewEvent(at, severity, component, code, diagnostics.Context{Operation: diagnostics.OperationCommand})
				if eventErr != nil {
					t.Fatalf("NewEvent(%q, %q, %q) error = %v", component, code, severity, eventErr)
				}
				if err := foundation.RecordDiagnostic(context.Background(), event); err != nil {
					t.Fatalf("RecordDiagnostic(%q, %q, %q) error = %v", component, code, severity, err)
				}
			}
		}
	}
	aggregates, err := foundation.ListDiagnosticAggregates(context.Background())
	if err != nil {
		t.Fatalf("ListDiagnosticAggregates() error = %v", err)
	}
	if len(aggregates) > diagnostics.MaxAggregateCount {
		t.Fatalf("aggregate count = %d, want <= %d", len(aggregates), diagnostics.MaxAggregateCount)
	}
}

func TestListDiagnosticAggregatesExcludesExpiredRowsWithoutAWrite(t *testing.T) {
	clock := fixedStoreClock{now: time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)}
	foundation, err := OpenWithOptions(Options{Path: t.TempDir() + "/codex-folio.sqlite3", Clock: clock})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	defer func() { _ = foundation.Close() }()

	at := clock.now.Add(-diagnostics.DefaultRetention - time.Hour)
	event, err := diagnostics.NewEvent(at, diagnostics.SeverityError, diagnostics.ComponentStore, apperrors.StoreReadFailed, diagnostics.Context{Operation: diagnostics.OperationOpenStore})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if err := foundation.RecordDiagnostic(context.Background(), event); err != nil {
		t.Fatalf("RecordDiagnostic() error = %v", err)
	}

	aggregates, err := foundation.ListDiagnosticAggregates(context.Background())
	if err != nil {
		t.Fatalf("ListDiagnosticAggregates() error = %v", err)
	}
	if len(aggregates) != 0 {
		t.Fatalf("aggregates after read-time expiry = %#v, want empty", aggregates)
	}
}

func TestRecordDiagnosticRejectsUnregisteredCodesBeforeWriting(t *testing.T) {
	foundation, err := Open(t.TempDir() + "/codex-folio.sqlite3")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = foundation.Close() }()

	event := diagnostics.Event{
		Time:      time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC),
		Severity:  diagnostics.SeverityError,
		Component: diagnostics.ComponentStore,
		ErrorCode: "CF_STORE_NOT_REGISTERED",
	}
	if err := foundation.RecordDiagnostic(context.Background(), event); err == nil || apperrors.Code(err) != apperrors.DiagnosticsEventInvalid {
		t.Fatalf("RecordDiagnostic() error = %v, want diagnostics event-invalid", err)
	}
}

func TestDiagnosticSettingsDefaultPersistAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex-folio.sqlite3")
	state, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if got, err := state.DiagnosticSettings(context.Background()); err != nil || got != diagnostics.DefaultSettings() {
		t.Fatalf("DiagnosticSettings() = %#v, %v", got, err)
	}
	want := diagnostics.Settings{Enabled: false, MinimumLevel: diagnostics.LevelWarning, RetentionDays: 7}
	if got, err := state.SetDiagnosticSettings(context.Background(), want); err != nil || got != want {
		t.Fatalf("SetDiagnosticSettings() = %#v, %v", got, err)
	}
	if err := state.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("Open(reopen) error = %v", err)
	}
	defer reopened.Close()
	if got, err := reopened.DiagnosticSettings(context.Background()); err != nil || got != want {
		t.Fatalf("reopened DiagnosticSettings() = %#v, %v", got, err)
	}
	for _, invalid := range []diagnostics.Settings{
		{Enabled: true, MinimumLevel: "debug", RetentionDays: 14},
		{Enabled: true, MinimumLevel: diagnostics.LevelInfo, RetentionDays: 0},
		{Enabled: true, MinimumLevel: diagnostics.LevelInfo, RetentionDays: diagnostics.MaximumRetentionDays + 1},
	} {
		if _, err := reopened.SetDiagnosticSettings(context.Background(), invalid); err == nil || apperrors.Code(err) != apperrors.DiagnosticsConfigurationInvalid {
			t.Fatalf("SetDiagnosticSettings(%#v) error = %v", invalid, err)
		}
	}
	if _, err := reopened.db.Exec(`UPDATE settings SET diagnostics_retention_days = 31 WHERE settings_id = 1`); err == nil {
		t.Fatal("SQLite settings boundary accepted retention above the supported maximum")
	}
}

func TestDiagnosticPersistenceHonorsDisablementAndMinimumLevel(t *testing.T) {
	clock := fixedStoreClock{now: time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)}
	state, err := OpenWithOptions(Options{Path: filepath.Join(t.TempDir(), "codex-folio.sqlite3"), Clock: clock})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer state.Close()
	record := func(severity diagnostics.Severity, code string) {
		event, eventErr := diagnostics.NewEvent(clock.now, severity, diagnostics.ComponentStore, code, diagnostics.Context{Operation: diagnostics.OperationOpenStore})
		if eventErr != nil {
			t.Fatalf("NewEvent() error = %v", eventErr)
		}
		if eventErr = state.RecordDiagnostic(context.Background(), event); eventErr != nil {
			t.Fatalf("RecordDiagnostic() error = %v", eventErr)
		}
	}
	if _, err := state.SetDiagnosticSettings(context.Background(), diagnostics.Settings{Enabled: true, MinimumLevel: diagnostics.LevelWarning, RetentionDays: 14}); err != nil {
		t.Fatalf("SetDiagnosticSettings(warning) error = %v", err)
	}
	record(diagnostics.SeverityInfo, apperrors.StoreOpenFailed)
	record(diagnostics.SeverityWarning, apperrors.StoreReadFailed)
	record(diagnostics.SeverityError, apperrors.StoreWriteFailed)
	aggregates, err := state.ListDiagnosticAggregates(context.Background())
	if err != nil {
		t.Fatalf("ListDiagnosticAggregates() error = %v", err)
	}
	if len(aggregates) != 2 {
		t.Fatalf("aggregates = %#v, want warning and error only", aggregates)
	}
	if _, err := state.SetDiagnosticSettings(context.Background(), diagnostics.Settings{Enabled: false, MinimumLevel: diagnostics.LevelInfo, RetentionDays: 14}); err != nil {
		t.Fatalf("SetDiagnosticSettings(disabled) error = %v", err)
	}
	record(diagnostics.SeverityError, apperrors.StoreIntegrityFailed)
	aggregates, err = state.ListDiagnosticAggregates(context.Background())
	if err != nil || len(aggregates) != 2 {
		t.Fatalf("aggregates after disabled record = %#v, %v", aggregates, err)
	}
}

func TestDiagnosticPersistenceEnforcesConfiguredAge(t *testing.T) {
	clock := fixedStoreClock{now: time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)}
	state, err := OpenWithOptions(Options{Path: filepath.Join(t.TempDir(), "codex-folio.sqlite3"), Clock: clock})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer state.Close()
	if _, err := state.SetDiagnosticSettings(context.Background(), diagnostics.Settings{Enabled: true, MinimumLevel: diagnostics.LevelInfo, RetentionDays: 1}); err != nil {
		t.Fatalf("SetDiagnosticSettings() error = %v", err)
	}
	for _, at := range []time.Time{clock.now.Add(-25 * time.Hour), clock.now.Add(-23 * time.Hour)} {
		event, eventErr := diagnostics.NewEvent(at, diagnostics.SeverityError, diagnostics.ComponentStore, apperrors.StoreReadFailed, diagnostics.Context{})
		if eventErr != nil {
			t.Fatalf("NewEvent() error = %v", eventErr)
		}
		if eventErr = state.RecordDiagnostic(context.Background(), event); eventErr != nil {
			t.Fatalf("RecordDiagnostic() error = %v", eventErr)
		}
	}
	var rows int
	if err := state.db.QueryRow(`SELECT count(*) FROM diagnostic_aggregates`).Scan(&rows); err != nil {
		t.Fatalf("count diagnostics: %v", err)
	}
	if rows != 1 {
		t.Fatalf("stored rows = %d, want only in-retention aggregate", rows)
	}
}

func TestBoundedDiagnosticsMigrationDefaultsExistingNullableRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex-folio.sqlite3")
	injected := errors.New("stop before diagnostics migration")
	state, err := OpenWithOptions(Options{Path: path, MigrationHook: MigrationHooks{Before: func(version int) error {
		if version == 22 {
			return injected
		}
		return nil
	}}})
	if state != nil {
		state.Close()
	}
	if !errors.Is(err, injected) {
		t.Fatalf("OpenWithOptions() error = %v, want injected", err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := database.Exec(`INSERT INTO settings (settings_id, diagnostics_retention_days, updated_at) VALUES (1, NULL, '2026-09-19T12:00:00Z')`); err != nil {
		database.Close()
		t.Fatalf("insert v21 settings: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close v21 database: %v", err)
	}
	migrated, err := Open(path)
	if err != nil {
		t.Fatalf("Open(migrate) error = %v", err)
	}
	defer migrated.Close()
	if got, err := migrated.DiagnosticSettings(context.Background()); err != nil || got != diagnostics.DefaultSettings() {
		t.Fatalf("migrated DiagnosticSettings() = %#v, %v", got, err)
	}
}
