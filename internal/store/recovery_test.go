package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/vault"
)

func TestRecoveryRotatesThreeValidatedCandidates(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), DatabaseFileName)
	foundation, err := Open(databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := foundation.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	recovery, err := NewRecovery(RecoveryOptions{
		DatabasePath: databasePath,
		Clock:        fixedStoreClock{now: time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("NewRecovery() error = %v", err)
	}
	for index := 0; index < 4; index++ {
		if _, err := recovery.CreateCheckpoint(context.Background()); err != nil {
			t.Fatalf("CreateCheckpoint() #%d error = %v", index+1, err)
		}
	}

	candidates, err := recovery.ListCandidates(context.Background())
	if err != nil {
		t.Fatalf("ListCandidates() error = %v", err)
	}
	if len(candidates) != RecoveryBackupCount {
		t.Fatalf("candidate count = %d, want exactly %d", len(candidates), RecoveryBackupCount)
	}
	for index, candidate := range candidates {
		wantID := fmt.Sprintf("backup-%d", index+1)
		if candidate.ID != wantID {
			t.Errorf("candidate[%d].ID = %q, want %q", index, candidate.ID, wantID)
		}
		if !candidate.Valid {
			t.Errorf("candidate[%d] is invalid: %q", index, candidate.ValidationCode)
		}
		if candidate.SchemaVersion != CurrentSchemaVersion {
			t.Errorf("candidate[%d].SchemaVersion = %d, want %d", index, candidate.SchemaVersion, CurrentSchemaVersion)
		}
	}
}

func TestRecoveryRotationFailureRetainsSparseKnownGoodCandidate(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), DatabaseFileName)
	foundation, err := Open(databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	for index := 0; index < RecoveryBackupCount; index++ {
		if _, err := foundation.CreateRecoveryCheckpoint(context.Background()); err != nil {
			_ = foundation.Close()
			t.Fatalf("CreateRecoveryCheckpoint() #%d error = %v", index+1, err)
		}
	}
	if err := foundation.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	recovery, err := NewRecovery(RecoveryOptions{DatabasePath: databasePath})
	if err != nil {
		t.Fatalf("NewRecovery() error = %v", err)
	}
	for _, id := range []string{"backup-1", "backup-2"} {
		if err := os.Remove(recovery.backupPath(id)); err != nil {
			t.Fatalf("remove %s: %v", id, err)
		}
		if err := os.Remove(recovery.metadataPath(id)); err != nil {
			t.Fatalf("remove %s metadata: %v", id, err)
		}
	}
	knownGood, err := os.ReadFile(recovery.backupPath("backup-3"))
	if err != nil {
		t.Fatalf("ReadFile() known-good candidate error = %v", err)
	}

	injected := errors.New("injected rotation failure")
	failureCount := 1
	if isWindowsRecoveryPath() {
		failureCount = 2
	}
	failing, err := NewRecovery(RecoveryOptions{
		DatabasePath: databasePath,
		FileSystem: &failingRenameFileSystem{
			FileSystem: recoveryFileSystem{},
			failures:   failureCount,
			err:        injected,
		},
	})
	if err != nil {
		t.Fatalf("NewRecovery() failing rotation error = %v", err)
	}
	if _, err := failing.CreateCheckpoint(context.Background()); err == nil {
		t.Fatal("CreateCheckpoint() succeeded after injected rotation failure")
	} else if got := apperrors.Code(err); got != apperrors.StoreBackupFailed {
		t.Fatalf("CreateCheckpoint() error code = %q, want %q", got, apperrors.StoreBackupFailed)
	}

	candidates, err := failing.ListCandidates(context.Background())
	if err != nil {
		t.Fatalf("ListCandidates() error = %v", err)
	}
	if len(candidates) != 1 || candidates[0].ID != "backup-3" || !candidates[0].Valid {
		t.Fatalf("candidates after rotation failure = %#v, want valid backup-3", candidates)
	}
	got, err := os.ReadFile(recovery.backupPath("backup-3"))
	if err != nil {
		t.Fatalf("ReadFile() retained candidate error = %v", err)
	}
	if !bytes.Equal(got, knownGood) {
		t.Fatal("failed rotation changed the only sparse known-good candidate")
	}
}

func TestRecoveryRestoresAnIntentionalCandidateAndPreservesActiveState(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), DatabaseFileName)
	key := bytes.Repeat([]byte{0x37}, 32)
	secureVault, err := vault.NewMemoryVault(vault.MemoryVaultOptions{Key: key, Generation: "recovery-generation"})
	if err != nil {
		t.Fatalf("NewMemoryVault() error = %v", err)
	}

	first, err := OpenWithVault(databasePath, secureVault)
	if err != nil {
		t.Fatalf("OpenWithVault() first error = %v", err)
	}
	firstCheckpoint := Checkpoint{
		CheckpointID: "checkpoint-first",
		Status:       "approved",
		Goal:         stringPointer("first checkpoint"),
		CreatedAt:    time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC),
	}
	if err := first.PutCheckpoint(context.Background(), firstCheckpoint); err != nil {
		t.Fatalf("PutCheckpoint() first error = %v", err)
	}
	if _, err := first.CreateRecoveryCheckpoint(context.Background()); err != nil {
		t.Fatalf("CreateRecoveryCheckpoint() first error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() first error = %v", err)
	}

	second, err := OpenWithVault(databasePath, secureVault)
	if err != nil {
		t.Fatalf("OpenWithVault() second error = %v", err)
	}
	secondCheckpoint := Checkpoint{
		CheckpointID: "checkpoint-second",
		Status:       "approved",
		Goal:         stringPointer("second checkpoint"),
		CreatedAt:    time.Date(2026, time.September, 1, 12, 1, 0, 0, time.UTC),
	}
	if err := second.PutCheckpoint(context.Background(), secondCheckpoint); err != nil {
		t.Fatalf("PutCheckpoint() second error = %v", err)
	}
	if _, err := second.CreateRecoveryCheckpoint(context.Background()); err != nil {
		t.Fatalf("CreateRecoveryCheckpoint() second error = %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close() second error = %v", err)
	}

	recovery, err := NewRecovery(RecoveryOptions{DatabasePath: databasePath})
	if err != nil {
		t.Fatalf("NewRecovery() error = %v", err)
	}
	result, err := recovery.Restore(context.Background(), "backup-2")
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if result.CandidateID != "backup-2" {
		t.Fatalf("CandidateID = %q, want backup-2", result.CandidateID)
	}
	if result.PreservedDatabaseID == "" {
		t.Fatal("Restore() did not report a preserved active database")
	}
	if result.KnownLossWindowStart.IsZero() || result.KnownLossWindowEnd.Before(result.KnownLossWindowStart) {
		t.Fatalf("Restore() loss window = %v..%v, want an ordered non-zero window", result.KnownLossWindowStart, result.KnownLossWindowEnd)
	}

	restored, err := OpenWithVault(databasePath, secureVault)
	if err != nil {
		t.Fatalf("OpenWithVault() restored error = %v", err)
	}
	defer func() { _ = restored.Close() }()
	got, err := restored.GetCheckpoint(context.Background(), firstCheckpoint.CheckpointID)
	if err != nil {
		t.Fatalf("GetCheckpoint() restored error = %v", err)
	}
	if got.Goal == nil || *got.Goal != *firstCheckpoint.Goal {
		t.Fatalf("restored checkpoint goal = %#v, want %q", got.Goal, *firstCheckpoint.Goal)
	}
	if _, err := restored.GetCheckpoint(context.Background(), secondCheckpoint.CheckpointID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetCheckpoint() displaced state error = %v, want sql.ErrNoRows", err)
	}
}

func TestRecoveryListsCorruptCandidatesAndRestoresOverCorruptActiveState(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), DatabaseFileName)
	foundation, err := Open(databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := foundation.CreateRecoveryCheckpoint(context.Background()); err != nil {
		t.Fatalf("CreateRecoveryCheckpoint() error = %v", err)
	}
	if err := foundation.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	recovery, err := NewRecovery(RecoveryOptions{DatabasePath: databasePath})
	if err != nil {
		t.Fatalf("NewRecovery() error = %v", err)
	}
	candidatePath := recovery.backupPath("backup-1")
	originalActive, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("ReadFile() active error = %v", err)
	}
	if err := os.WriteFile(candidatePath, []byte("corrupt backup"), 0o600); err != nil {
		t.Fatalf("WriteFile() candidate error = %v", err)
	}
	candidates, err := recovery.ListCandidates(context.Background())
	if err != nil {
		t.Fatalf("ListCandidates() error = %v", err)
	}
	if len(candidates) == 0 || candidates[0].Valid || candidates[0].ValidationCode != "CF_STORE_RECOVERY_CANDIDATE_INVALID" {
		t.Fatalf("corrupt candidate = %#v, want invalid candidate code", candidates)
	}
	if _, err := recovery.Restore(context.Background(), "backup-1"); err == nil {
		t.Fatal("Restore() accepted a corrupt candidate")
	} else if got := apperrors.Code(err); got != apperrors.StoreRecoveryCandidateInvalid {
		t.Fatalf("Restore() corrupt candidate code = %q, want %q", got, apperrors.StoreRecoveryCandidateInvalid)
	}
	activeAfterRejectedRestore, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("ReadFile() after rejected restore error = %v", err)
	}
	if !bytes.Equal(activeAfterRejectedRestore, originalActive) {
		t.Fatal("rejected restore changed the active database")
	}

	if err := os.WriteFile(candidatePath, originalActive, 0o600); err != nil {
		t.Fatalf("WriteFile() valid candidate error = %v", err)
	}
	if err := os.WriteFile(databasePath, []byte("corrupt active"), 0o600); err != nil {
		t.Fatalf("WriteFile() active corruption error = %v", err)
	}
	verification, err := recovery.VerifyActive(context.Background())
	if verification.Valid || err == nil || apperrors.Code(err) != apperrors.StoreIntegrityFailed {
		t.Fatalf("VerifyActive() = %#v, %v; want integrity failure", verification, err)
	}
	result, err := recovery.Restore(context.Background(), "backup-1")
	if err != nil {
		t.Fatalf("Restore() over corrupt active error = %v", err)
	}
	if !strings.HasPrefix(result.PreservedDatabaseID, "damaged-") {
		t.Fatalf("PreservedDatabaseID = %q, want damaged preservation", result.PreservedDatabaseID)
	}
	restoredActive, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("ReadFile() restored active error = %v", err)
	}
	if !bytes.Equal(restoredActive, originalActive) {
		t.Fatal("restore did not activate the validated candidate")
	}
}

func TestRecoveryFailureDuringBackupOrRestoreLeavesKnownGoodStateAvailable(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), DatabaseFileName)
	foundation, err := Open(databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := foundation.CreateRecoveryCheckpoint(context.Background()); err != nil {
		t.Fatalf("CreateRecoveryCheckpoint() error = %v", err)
	}
	if err := foundation.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	previousCandidate, err := os.ReadFile(filepath.Join(filepath.Dir(databasePath), RecoveryDirectoryName, "codex-folio.sqlite3.backup-1"))
	if err != nil {
		t.Fatalf("ReadFile() previous candidate error = %v", err)
	}
	injected := errors.New("injected recovery boundary failure")
	failingBackup, err := NewRecovery(RecoveryOptions{
		DatabasePath: databasePath,
		Hooks: RecoveryHooks{
			AfterBackupCopy: func(BackupReason) error { return injected },
		},
	})
	if err != nil {
		t.Fatalf("NewRecovery() failing backup error = %v", err)
	}
	if _, err := failingBackup.CreateCheckpoint(context.Background()); err == nil {
		t.Fatal("CreateCheckpoint() succeeded after injected backup failure")
	} else if got := apperrors.Code(err); got != apperrors.StoreBackupFailed {
		t.Fatalf("CreateCheckpoint() error code = %q, want %q", got, apperrors.StoreBackupFailed)
	}
	unchangedCandidate, err := os.ReadFile(filepath.Join(filepath.Dir(databasePath), RecoveryDirectoryName, "codex-folio.sqlite3.backup-1"))
	if err != nil {
		t.Fatalf("ReadFile() unchanged candidate error = %v", err)
	}
	if !bytes.Equal(unchangedCandidate, previousCandidate) {
		t.Fatal("failed backup displaced the previous known-good candidate")
	}

	activeBeforeRestore, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("ReadFile() active before restore error = %v", err)
	}
	failingRestore, err := NewRecovery(RecoveryOptions{
		DatabasePath: databasePath,
		Hooks: RecoveryHooks{
			BeforeRestoreActivation: func(string) error { return injected },
		},
	})
	if err != nil {
		t.Fatalf("NewRecovery() failing restore error = %v", err)
	}
	if _, err := failingRestore.Restore(context.Background(), "backup-1"); err == nil {
		t.Fatal("Restore() succeeded after injected pre-activation failure")
	} else if got := apperrors.Code(err); got != apperrors.StoreRecoveryRestoreFailed {
		t.Fatalf("Restore() error code = %q, want %q", got, apperrors.StoreRecoveryRestoreFailed)
	}
	activeAfterRestoreFailure, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("ReadFile() active after restore error = %v", err)
	}
	if !bytes.Equal(activeAfterRestoreFailure, activeBeforeRestore) {
		t.Fatal("failed restore changed the active database")
	}
}

func TestRecoveryRollsBackAfterActivationFailure(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), DatabaseFileName)
	foundation, err := Open(databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := foundation.CreateRecoveryCheckpoint(context.Background()); err != nil {
		t.Fatalf("CreateRecoveryCheckpoint() error = %v", err)
	}
	if err := foundation.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := database.ExecContext(context.Background(), `INSERT INTO diagnostic_aggregates (
		diagnostic_aggregate_id, component, error_code, severity, occurrence_count, first_seen_at, last_seen_at
	) VALUES ('active-only', 'store', 'CF_STORE_WRITE_FAILED', 'error', 1, '2026-09-01T12:00:00Z', '2026-09-01T12:00:00Z')`); err != nil {
		t.Fatalf("insert active sentinel: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close active sentinel database: %v", err)
	}
	activeBeforeRestore, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("ReadFile() active before restore error = %v", err)
	}
	injected := errors.New("injected post-activation failure")
	recovery, err := NewRecovery(RecoveryOptions{
		DatabasePath: databasePath,
		Hooks: RecoveryHooks{
			AfterRestoreActivation: func(string) error { return injected },
		},
	})
	if err != nil {
		t.Fatalf("NewRecovery() error = %v", err)
	}
	if _, err := recovery.Restore(context.Background(), "backup-1"); err == nil {
		t.Fatal("Restore() succeeded after injected post-activation failure")
	} else if got := apperrors.Code(err); got != apperrors.StoreRecoveryRestoreFailed {
		t.Fatalf("Restore() error code = %q, want %q", got, apperrors.StoreRecoveryRestoreFailed)
	}
	activeAfterRollback, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("ReadFile() active after rollback error = %v", err)
	}
	if !bytes.Equal(activeAfterRollback, activeBeforeRestore) {
		t.Fatal("restore failure did not roll the active database back")
	}
}

func TestMigrationBackupFailureStopsBeforeSchemaMutation(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), DatabaseFileName)
	injected := errors.New("injected backup failure")
	foundation, err := OpenWithOptions(Options{
		Path: databasePath,
		RecoveryHooks: RecoveryHooks{
			BeforeBackup: func(BackupReason) error { return injected },
		},
	})
	if foundation != nil {
		_ = foundation.Close()
		t.Fatal("OpenWithOptions() returned a store after backup failure")
	}
	if err == nil || !errors.Is(err, injected) {
		t.Fatalf("OpenWithOptions() error = %v, want injected backup failure", err)
	}
	if got := apperrors.Code(err); got != apperrors.StoreBackupFailed {
		t.Fatalf("OpenWithOptions() error code = %q, want %q", got, apperrors.StoreBackupFailed)
	}

	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = database.Close() }()
	if names := queryObjectNames(t, database, "table"); len(names) != 0 {
		t.Fatalf("tables after backup failure = %v, want empty database", names)
	}
	recovery, err := NewRecovery(RecoveryOptions{DatabasePath: databasePath})
	if err != nil {
		t.Fatalf("NewRecovery() error = %v", err)
	}
	candidates, err := recovery.ListCandidates(context.Background())
	if err != nil {
		t.Fatalf("ListCandidates() error = %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates after failed backup = %#v, want none", candidates)
	}
}

func TestMigrationFailureRetainsThePreMigrationRecoveryPoint(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), DatabaseFileName)
	injected := errors.New("injected migration failure")
	foundation, err := OpenWithOptions(Options{
		Path: databasePath,
		MigrationHook: MigrationHooks{
			After: func(int) error { return injected },
		},
	})
	if foundation != nil {
		_ = foundation.Close()
		t.Fatal("OpenWithOptions() returned a store after migration failure")
	}
	if err == nil || !errors.Is(err, injected) || apperrors.Code(err) != apperrors.StoreMigrationFailed {
		t.Fatalf("OpenWithOptions() error = %v, want coded migration failure", err)
	}

	recovery, err := NewRecovery(RecoveryOptions{DatabasePath: databasePath})
	if err != nil {
		t.Fatalf("NewRecovery() error = %v", err)
	}
	candidates, err := recovery.ListCandidates(context.Background())
	if err != nil {
		t.Fatalf("ListCandidates() error = %v", err)
	}
	if len(candidates) != 1 || !candidates[0].Valid || candidates[0].SchemaVersion != 0 {
		t.Fatalf("pre-migration candidates = %#v, want one valid schema-zero candidate", candidates)
	}

	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("sql.Open() after migration failure error = %v", err)
	}
	defer func() { _ = database.Close() }()
	if names := queryObjectNames(t, database, "table"); len(names) != 0 {
		t.Fatalf("tables after migration rollback = %v, want empty database", names)
	}
}

func TestMigrationFailureAfterCommitStillExposesTheOriginalCandidate(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), DatabaseFileName)
	injected := errors.New("injected activation boundary failure")
	foundation, err := OpenWithOptions(Options{
		Path: databasePath,
		RecoveryHooks: RecoveryHooks{
			AfterMigrationBeforeActivation: func(int) error { return injected },
		},
	})
	if foundation != nil {
		_ = foundation.Close()
		t.Fatal("OpenWithOptions() returned a store after activation-boundary failure")
	}
	if err == nil || !errors.Is(err, injected) || apperrors.Code(err) != apperrors.StoreMigrationFailed {
		t.Fatalf("OpenWithOptions() error = %v, want coded migration failure", err)
	}
	recovery, err := NewRecovery(RecoveryOptions{DatabasePath: databasePath})
	if err != nil {
		t.Fatalf("NewRecovery() error = %v", err)
	}
	candidates, err := recovery.ListCandidates(context.Background())
	if err != nil {
		t.Fatalf("ListCandidates() error = %v", err)
	}
	if len(candidates) != 1 || !candidates[0].Valid || candidates[0].SchemaVersion != 0 {
		t.Fatalf("activation-boundary candidates = %#v, want original schema-zero candidate", candidates)
	}
}

func TestRecoveryBackupsRetainCiphertextWithoutVaultMaterial(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), DatabaseFileName)
	key := bytes.Repeat([]byte{0x71}, 32)
	secureVault, err := vault.NewMemoryVault(vault.MemoryVaultOptions{Key: key, Generation: "backup-generation"})
	if err != nil {
		t.Fatalf("NewMemoryVault() error = %v", err)
	}
	foundation, err := OpenWithVault(databasePath, secureVault)
	if err != nil {
		t.Fatalf("OpenWithVault() error = %v", err)
	}
	identity := ProjectIdentity{
		ProjectIdentityID: "backup-project",
		ProjectAlias:      "backup-alias",
		CanonicalPath:     "/private/recovery/backup-sentinel",
		CreatedAt:         time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC),
	}
	if err := foundation.PutProjectIdentity(context.Background(), identity); err != nil {
		t.Fatalf("PutProjectIdentity() error = %v", err)
	}
	checkpoint := Checkpoint{
		CheckpointID:     "backup-checkpoint",
		Status:           "approved",
		Goal:             stringPointer("backup plaintext sentinel"),
		RecoveryMetadata: stringPointer("backup recovery sentinel"),
		CreatedAt:        time.Date(2026, time.September, 1, 12, 1, 0, 0, time.UTC),
	}
	if err := foundation.PutCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatalf("PutCheckpoint() error = %v", err)
	}
	if _, err := foundation.CreateRecoveryCheckpoint(context.Background()); err != nil {
		t.Fatalf("CreateRecoveryCheckpoint() error = %v", err)
	}
	if err := foundation.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	backupPath := filepath.Join(filepath.Dir(databasePath), RecoveryDirectoryName, "codex-folio.sqlite3.backup-1")
	backupBytes, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("ReadFile() backup error = %v", err)
	}
	for _, forbidden := range [][]byte{
		[]byte(identity.CanonicalPath),
		[]byte(*checkpoint.Goal),
		[]byte(*checkpoint.RecoveryMetadata),
		key,
	} {
		if bytes.Contains(backupBytes, forbidden) {
			t.Fatalf("backup contains forbidden sensitive bytes %q", forbidden)
		}
	}
}

type failingRenameFileSystem struct {
	FileSystem
	failures int
	err      error
}

func (filesystem *failingRenameFileSystem) Rename(sourcePath, destinationPath string) error {
	if filesystem.failures > 0 {
		filesystem.failures--
		return filesystem.err
	}
	return filesystem.FileSystem.Rename(sourcePath, destinationPath)
}
