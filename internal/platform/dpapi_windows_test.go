//go:build windows

package platform

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

func TestDPAPIVaultRoundTripsAndReusesProtectedKeyAcrossProviderRestart(t *testing.T) {
	root := t.TempDir()
	vaultPath := filepath.Join(root, "codex-folio.vault")
	plaintext := []byte("dpapi protected envelope sentinel")
	associatedData := []byte("codex-folio/project-identities/project-1/canonical-path")

	first, err := NewDPAPIVault(vaultPath)
	if err != nil {
		t.Fatalf("NewDPAPIVault() error = %v", err)
	}
	envelope, err := first.Encrypt(context.Background(), plaintext, associatedData)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	protectedFile, err := os.ReadFile(vaultPath)
	if err != nil {
		t.Fatalf("ReadFile(vault) error = %v", err)
	}
	if bytes.Contains(protectedFile, plaintext) {
		t.Fatal("protected vault file contains the envelope plaintext")
	}
	if hasEveryone, err := windowsPathHasEveryoneACE(vaultPath); err != nil {
		t.Fatalf("inspect vault DACL: %v", err)
	} else if hasEveryone {
		t.Fatal("protected vault file grants access to Everyone")
	}

	second, err := NewDPAPIVault(vaultPath)
	if err != nil {
		t.Fatalf("NewDPAPIVault() after restart error = %v", err)
	}
	decoded, err := second.Decrypt(context.Background(), envelope, associatedData)
	if err != nil {
		t.Fatalf("Decrypt() after restart error = %v", err)
	}
	if !bytes.Equal(decoded, plaintext) {
		t.Fatalf("Decrypt() after restart = %q, want %q", decoded, plaintext)
	}
}

func TestDPAPIKeyProviderRejectsMalformedTamperedAndMissingProtectedMaterial(t *testing.T) {
	root := t.TempDir()

	malformedPath := filepath.Join(root, "malformed.vault")
	if err := os.WriteFile(malformedPath, []byte("malformed protected material"), 0o600); err != nil {
		t.Fatalf("WriteFile(malformed): %v", err)
	}
	if _, err := NewDPAPIVault(malformedPath); err == nil {
		t.Fatal("NewDPAPIVault() accepted malformed protected material")
	} else if got := apperrors.Code(err); got != apperrors.VaultKeyInvalid {
		t.Fatalf("malformed error code = %q, want %q", got, apperrors.VaultKeyInvalid)
	} else if errors.Is(err, ErrDPAPIProtectedMaterial) == false {
		t.Fatalf("malformed error = %v, want protected-material cause", err)
	}

	missingPath := filepath.Join(root, "missing.vault")
	if _, err := NewDPAPIVaultWithOptions(missingPath, DPAPIOptions{}); err == nil {
		t.Fatal("NewDPAPIVaultWithOptions() initialized missing material when creation was disabled")
	} else if got := apperrors.Code(err); got != apperrors.VaultUnavailable {
		t.Fatalf("missing error code = %q, want %q", got, apperrors.VaultUnavailable)
	}

	tamperedPath := filepath.Join(root, "tampered.vault")
	secureVault, err := NewDPAPIVault(tamperedPath)
	if err != nil {
		t.Fatalf("NewDPAPIVault() for tamper fixture error = %v", err)
	}
	if _, err := secureVault.Encrypt(context.Background(), []byte("sentinel"), nil); err != nil {
		t.Fatalf("Encrypt() for tamper fixture error = %v", err)
	}
	tampered, err := os.ReadFile(tamperedPath)
	if err != nil {
		t.Fatalf("ReadFile(tamper fixture): %v", err)
	}
	tampered[len(tampered)-1] ^= 0x01
	if err := os.WriteFile(tamperedPath, tampered, 0o600); err != nil {
		t.Fatalf("WriteFile(tamper fixture): %v", err)
	}
	if _, err := NewDPAPIVault(tamperedPath); err == nil {
		t.Fatal("NewDPAPIVault() accepted tampered protected material")
	} else if got := apperrors.Code(err); got != apperrors.VaultKeyInvalid {
		t.Fatalf("tampered error code = %q, want %q", got, apperrors.VaultKeyInvalid)
	}

	metadataTamperedPath := filepath.Join(root, "metadata-tampered.vault")
	if _, err := NewDPAPIVault(metadataTamperedPath); err != nil {
		t.Fatalf("NewDPAPIVault() for metadata tamper fixture error = %v", err)
	}
	metadataTampered, err := os.ReadFile(metadataTamperedPath)
	if err != nil {
		t.Fatalf("ReadFile(metadata tamper fixture): %v", err)
	}
	if metadataTampered[dpapiRecordHeaderSize] == '0' {
		metadataTampered[dpapiRecordHeaderSize] = '1'
	} else {
		metadataTampered[dpapiRecordHeaderSize] = '0'
	}
	if err := os.WriteFile(metadataTamperedPath, metadataTampered, 0o600); err != nil {
		t.Fatalf("WriteFile(metadata tamper fixture): %v", err)
	}
	if _, err := NewDPAPIVault(metadataTamperedPath); err == nil {
		t.Fatal("NewDPAPIVault() accepted tampered generation metadata")
	} else if got := apperrors.Code(err); got != apperrors.VaultKeyInvalid {
		t.Fatalf("metadata tampered error code = %q, want %q", got, apperrors.VaultKeyInvalid)
	}

	provider, err := NewDPAPIKeyProvider(tamperedPath)
	if err != nil {
		t.Fatalf("NewDPAPIKeyProvider() error = %v", err)
	}
	if _, err := provider.LoadOrCreate(context.Background()); err == nil {
		t.Fatal("LoadOrCreate() accepted tampered protected material")
	}
}

func TestDPAPIProviderDoesNotRecreateMaterialAfterItDisappears(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "codex-folio.vault")
	secureVault, err := NewDPAPIVault(vaultPath)
	if err != nil {
		t.Fatalf("NewDPAPIVault() error = %v", err)
	}
	if err := os.Remove(vaultPath); err != nil {
		t.Fatalf("Remove(vault): %v", err)
	}
	if _, err := secureVault.Encrypt(context.Background(), []byte("sentinel"), nil); err == nil {
		t.Fatal("Encrypt() recreated missing protected material")
	} else if got := apperrors.Code(err); got != apperrors.VaultUnavailable {
		t.Fatalf("missing-after-initialization error code = %q, want %q", got, apperrors.VaultUnavailable)
	}
}
