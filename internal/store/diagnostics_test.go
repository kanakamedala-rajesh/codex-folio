package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
)

func TestDiagnosticAggregatesPersistOnlyRedactedBoundedBuckets(t *testing.T) {
	databasePath := t.TempDir() + "/codex-folio.sqlite3"
	foundation, err := Open(databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = foundation.Close() }()

	at := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
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
	foundation, err := Open(t.TempDir() + "/codex-folio.sqlite3")
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
	at := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
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
