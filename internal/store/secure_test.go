package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/continuation"
	"venkatasudha.com/codex-folio/internal/vault"
)

func TestSensitiveRecordsRoundTripOnlyThroughTheVaultBoundary(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "sensitive.sqlite3")
	key := bytes.Repeat([]byte{0x5c}, 32)
	secureVault, err := vault.NewMemoryVault(vault.MemoryVaultOptions{
		Key:        key,
		Generation: "generation-1",
	})
	if err != nil {
		t.Fatalf("NewMemoryVault() error = %v", err)
	}
	foundation, err := OpenWithOptions(Options{Path: databasePath, Vault: secureVault})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}

	project := ProjectIdentity{
		ProjectIdentityID: "project-1",
		ProjectAlias:      "example",
		CanonicalPath:     "/private/sentinel/project",
		CreatedAt:         time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, time.August, 31, 12, 1, 0, 0, time.UTC),
	}
	if err := foundation.PutProjectIdentity(context.Background(), project); err != nil {
		_ = foundation.Close()
		t.Fatalf("PutProjectIdentity() error = %v", err)
	}
	gotProject, err := foundation.GetProjectIdentity(context.Background(), project.ProjectIdentityID)
	if err != nil {
		_ = foundation.Close()
		t.Fatalf("GetProjectIdentity() error = %v", err)
	}
	if !reflect.DeepEqual(gotProject, project) {
		t.Fatalf("GetProjectIdentity() = %#v, want %#v", gotProject, project)
	}

	checkpoint := Checkpoint{
		CheckpointID:      "checkpoint-1",
		ProjectIdentityID: "project-1",
		Status:            "approved",
		Goal:              stringPointer("goal sentinel"),
		CompletedWork:     stringPointer("completed sentinel"),
		PendingWork:       stringPointer("pending sentinel"),
		Validation:        stringPointer("validation sentinel"),
		Risks:             stringPointer("risks sentinel"),
		NextAction:        stringPointer("next action sentinel"),
		RecoveryMetadata:  stringPointer("recovery sentinel"),
		CreatedAt:         time.Date(2026, time.August, 31, 12, 2, 0, 0, time.UTC),
		ExpiresAt:         timePointer(time.Date(2026, time.September, 7, 12, 2, 0, 0, time.UTC)),
	}
	if err := foundation.PutCheckpoint(context.Background(), checkpoint); err != nil {
		_ = foundation.Close()
		t.Fatalf("PutCheckpoint() error = %v", err)
	}
	gotCheckpoint, err := foundation.GetCheckpoint(context.Background(), checkpoint.CheckpointID)
	if err != nil {
		_ = foundation.Close()
		t.Fatalf("GetCheckpoint() error = %v", err)
	}
	if !reflect.DeepEqual(gotCheckpoint, checkpoint) {
		t.Fatalf("GetCheckpoint() = %#v, want %#v", gotCheckpoint, checkpoint)
	}

	if err := foundation.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	databaseBytes, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, forbidden := range [][]byte{
		[]byte(project.CanonicalPath),
		[]byte("goal sentinel"),
		[]byte("recovery sentinel"),
		key,
	} {
		if bytes.Contains(databaseBytes, forbidden) {
			t.Fatalf("database contains forbidden sensitive bytes %q", forbidden)
		}
	}
	reopened, err := OpenWithOptions(Options{Path: databasePath, Vault: secureVault})
	if err != nil {
		t.Fatalf("reopen sensitive store: %v", err)
	}
	defer reopened.Close()
	records, err := reopened.ListProjectRecords(context.Background())
	if err != nil || len(records) != 1 || records[0].ID != project.ProjectIdentityID || records[0].Alias != project.ProjectAlias || records[0].CanonicalPath != project.CanonicalPath {
		t.Fatalf("ListProjectRecords() after restart = %#v, %v", records, err)
	}
}

func TestContinuationCheckpointMetadataUsesEncryptedCheckpointStorage(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "continuation.sqlite3")
	secureVault, err := vault.NewMemoryVault(vault.MemoryVaultOptions{Key: bytes.Repeat([]byte{0x4c}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	foundation, err := OpenWithOptions(Options{Path: databasePath, Vault: secureVault})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	metadata := `{"goal":"checkpoint metadata sentinel"}`
	record := continuation.CheckpointRecord{ID: "checkpoint-1", Status: continuation.StatusDraft, Metadata: metadata, CreatedAt: now}
	if err := foundation.SaveCheckpoint(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	got, err := foundation.LoadCheckpoint(context.Background(), record.ID)
	if err != nil || got.Metadata != metadata {
		t.Fatalf("LoadCheckpoint() = %#v, %v", got, err)
	}
	edited := "approved checkpoint metadata sentinel"
	record.Status, record.Metadata, record.Goal = continuation.StatusApproved, edited, &edited
	if err := foundation.SaveCheckpoint(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	got, err = foundation.LoadCheckpoint(context.Background(), record.ID)
	if err != nil || got.Status != continuation.StatusApproved || got.Metadata != edited || got.Goal == nil || *got.Goal != edited {
		t.Fatalf("approved LoadCheckpoint() = %#v, %v", got, err)
	}
	if err := foundation.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(database, []byte("checkpoint metadata sentinel")) || bytes.Contains(database, []byte(edited)) {
		t.Fatal("database contains continuation metadata in plaintext")
	}
}

func TestCheckpointInventoryIsNewestFirstAndExactPurgePreservesUnmatchedData(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "checkpoint-inventory.sqlite3")
	secureVault, err := vault.NewMemoryVault(vault.MemoryVaultOptions{Key: bytes.Repeat([]byte{0x3c}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	foundation, err := OpenWithOptions(Options{Path: databasePath, Vault: secureVault})
	if err != nil {
		t.Fatal(err)
	}
	defer foundation.Close()
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	for _, record := range []continuation.CheckpointRecord{
		{ID: "older", Status: continuation.StatusCompleted, Metadata: `{"revision":"revision-1"}`, CreatedAt: now.Add(-time.Hour)},
		{ID: "newer", Status: continuation.StatusApproved, Metadata: `{"revision":"revision-2"}`, Goal: stringPointer("sanitized goal"), CreatedAt: now},
	} {
		if err := foundation.SaveCheckpoint(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := foundation.ListCheckpoints(context.Background())
	if err != nil || len(listed) != 2 || listed[0].ID != "newer" || listed[1].ID != "older" || listed[0].Goal == nil || *listed[0].Goal != "sanitized goal" {
		t.Fatalf("ListCheckpoints() = %#v, %v", listed, err)
	}
	if err := foundation.PurgeCheckpoint(context.Background(), "newer", "stale"); !errors.Is(err, continuation.ErrCheckpointRevisionChanged) {
		t.Fatalf("PurgeCheckpoint(stale) error = %v", err)
	}
	if err := foundation.PurgeCheckpoint(context.Background(), "newer", "revision-2"); err != nil {
		t.Fatalf("PurgeCheckpoint() error = %v", err)
	}
	if _, err := foundation.LoadCheckpoint(context.Background(), "newer"); !errors.Is(err, continuation.ErrCheckpointNotFound) {
		t.Fatalf("LoadCheckpoint(purged) error = %v", err)
	}
	if older, err := foundation.LoadCheckpoint(context.Background(), "older"); err != nil || older.ID != "older" {
		t.Fatalf("unmatched checkpoint = %#v, %v", older, err)
	}
}

func TestSensitiveRecordsFailClosedWithoutAVault(t *testing.T) {
	foundation, err := Open(filepath.Join(t.TempDir(), "missing-vault.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = foundation.Close() }()

	project := ProjectIdentity{
		ProjectIdentityID: "project-1",
		ProjectAlias:      "example",
		CanonicalPath:     "/private/sentinel/project",
		CreatedAt:         time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
	}
	if err := foundation.PutProjectIdentity(context.Background(), project); err == nil {
		t.Fatal("PutProjectIdentity() succeeded without a vault")
	} else if got := apperrors.Code(err); got != apperrors.VaultUnavailable {
		t.Fatalf("PutProjectIdentity() error code = %q, want %q", got, apperrors.VaultUnavailable)
	}
	if _, err := foundation.GetProjectIdentity(context.Background(), project.ProjectIdentityID); err == nil {
		t.Fatal("GetProjectIdentity() succeeded without a vault")
	} else if got := apperrors.Code(err); got != apperrors.VaultUnavailable {
		t.Fatalf("GetProjectIdentity() error code = %q, want %q", got, apperrors.VaultUnavailable)
	}
}

func TestProjectSafeProjectionAndAliasEditDoNotDecryptCanonicalPath(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "project-safe.sqlite3")
	secureVault, err := vault.NewMemoryVault(vault.MemoryVaultOptions{Key: bytes.Repeat([]byte{0x6c}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	withVault, err := OpenWithOptions(Options{Path: databasePath, Vault: secureVault})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	project := ProjectIdentity{ProjectIdentityID: "project-safe", ProjectAlias: "Before", RepositoryBasename: "project", CanonicalPath: "/private/sentinel/project", CreatedAt: now, UpdatedAt: now}
	if err := withVault.PutProjectIdentity(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	if err := withVault.Close(); err != nil {
		t.Fatal(err)
	}

	withoutVault, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := withoutVault.ListProjectIdentities(context.Background())
	if err != nil || len(projects) != 1 || projects[0].Alias != "Before" || projects[0].Basename != "project" {
		t.Fatalf("ListProjectIdentities() = %#v, %v", projects, err)
	}
	updatedAt := now.Add(time.Minute)
	if err := withoutVault.UpdateProjectAlias(context.Background(), project.ProjectIdentityID, "After", updatedAt); err != nil {
		t.Fatalf("UpdateProjectAlias() error = %v", err)
	}
	if _, err := withoutVault.ListProjectRecords(context.Background()); apperrors.Code(err) != apperrors.VaultUnavailable {
		t.Fatalf("private project read error = %v, want %s", err, apperrors.VaultUnavailable)
	}
	if err := withoutVault.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithOptions(Options{Path: databasePath, Vault: secureVault})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetProjectIdentity(context.Background(), project.ProjectIdentityID)
	if err != nil || got.ProjectAlias != "After" || got.CanonicalPath != project.CanonicalPath || !got.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("preserved Project Identity = %#v, %v", got, err)
	}
}

func TestSensitiveRecordsRejectTamperedAndWrongGenerationCiphertext(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "tampered.sqlite3")
	first, err := vault.NewMemoryVault(vault.MemoryVaultOptions{
		Key:        bytes.Repeat([]byte{0x5c}, 32),
		Generation: "generation-1",
	})
	if err != nil {
		t.Fatalf("NewMemoryVault() error = %v", err)
	}
	foundation, err := OpenWithOptions(Options{Path: databasePath, Vault: first})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	project := ProjectIdentity{
		ProjectIdentityID: "project-1",
		ProjectAlias:      "example",
		CanonicalPath:     "/private/sentinel/project",
		CreatedAt:         time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
	}
	if err := foundation.PutProjectIdentity(context.Background(), project); err != nil {
		_ = foundation.Close()
		t.Fatalf("PutProjectIdentity() error = %v", err)
	}
	var originalCiphertext []byte
	if err := foundation.db.QueryRowContext(context.Background(), `SELECT canonical_path_ciphertext FROM project_identities WHERE project_identity_id = ?`, project.ProjectIdentityID).Scan(&originalCiphertext); err != nil {
		_ = foundation.Close()
		t.Fatalf("read ciphertext fixture: %v", err)
	}
	if _, err := foundation.db.ExecContext(context.Background(), `UPDATE project_identities SET canonical_path_ciphertext = ? WHERE project_identity_id = ?`, []byte("tampered"), project.ProjectIdentityID); err != nil {
		_ = foundation.Close()
		t.Fatalf("tamper fixture update: %v", err)
	}
	if _, err := foundation.GetProjectIdentity(context.Background(), project.ProjectIdentityID); err == nil {
		t.Fatal("GetProjectIdentity() accepted tampered ciphertext")
	} else if got := apperrors.Code(err); got != apperrors.VaultEnvelopeInvalid {
		t.Fatalf("tampered store error code = %q, want %q", got, apperrors.VaultEnvelopeInvalid)
	}
	if _, err := foundation.db.ExecContext(context.Background(), `UPDATE project_identities SET canonical_path_ciphertext = ? WHERE project_identity_id = ?`, originalCiphertext, project.ProjectIdentityID); err != nil {
		t.Fatalf("restore ciphertext fixture: %v", err)
	}
	if err := foundation.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	second, err := vault.NewMemoryVault(vault.MemoryVaultOptions{
		Key:        bytes.Repeat([]byte{0x5c}, 32),
		Generation: "generation-2",
	})
	if err != nil {
		t.Fatalf("NewMemoryVault() second error = %v", err)
	}
	other, err := OpenWithOptions(Options{Path: databasePath, Vault: second})
	if err != nil {
		t.Fatalf("OpenWithOptions() second error = %v", err)
	}
	defer func() { _ = other.Close() }()

	if _, err := other.GetProjectIdentity(context.Background(), project.ProjectIdentityID); err == nil {
		t.Fatal("GetProjectIdentity() accepted ciphertext from another key generation")
	} else if got := apperrors.Code(err); got != apperrors.VaultKeyGenerationMismatch {
		t.Fatalf("generation mismatch store error code = %q, want %q", got, apperrors.VaultKeyGenerationMismatch)
	}
	if err := other.Close(); err != nil {
		t.Fatalf("Close() second error = %v", err)
	}
}

func stringPointer(value string) *string {
	return &value
}

func timePointer(value time.Time) *time.Time {
	return &value
}
