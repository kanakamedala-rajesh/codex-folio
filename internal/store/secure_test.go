package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
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
