package platform

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/vault"
)

func TestPassphraseVaultStartsLockedAndRoundTripsAcrossRestart(t *testing.T) {
	path := passphraseTestPath(t)
	key := bytes.Repeat([]byte{0x5a}, passphraseEnvelopeKeySize)
	passphrase := "correct horse battery staple"

	firstProvider, err := NewPassphraseKeyProviderWithOptions(path, PassphraseOptions{
		AllowCreate: true,
		Random:      bytes.NewReader(bytes.Repeat([]byte{0x31}, passphraseEnvelopeKeySize+passphraseGenerationSize+passphraseSaltSize+passphraseNonceSize)),
	})
	if err != nil {
		t.Fatalf("NewPassphraseKeyProviderWithOptions() error = %v", err)
	}
	if firstProvider.IsUnlocked() {
		t.Fatal("new passphrase provider is unlocked before explicit unlock")
	}
	if _, err := firstProvider.LoadOrCreate(context.Background()); err == nil {
		t.Fatal("LoadOrCreate() succeeded before explicit unlock")
	} else if got := apperrors.Code(err); got != apperrors.VaultLocked {
		t.Fatalf("locked error code = %q, want %q", got, apperrors.VaultLocked)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("vault file before explicit unlock error = %v, want not-exist", err)
	}

	if err := firstProvider.Unlock(context.Background(), passphrase); err != nil {
		t.Fatalf("Unlock() error = %v", err)
	}
	secureVault, err := vault.NewEnvelopeVault(firstProvider, vault.EnvelopeOptions{})
	if err != nil {
		t.Fatalf("NewEnvelopeVault() error = %v", err)
	}
	envelope, err := secureVault.Encrypt(context.Background(), key, []byte("test/passphrase"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if firstProvider.IsUnlocked() == false {
		t.Fatal("provider is locked after explicit unlock")
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(vault) error = %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("vault file permissions = %o, want 600", got)
	}
	protected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(vault) error = %v", err)
	}
	for name, forbidden := range map[string][]byte{
		"envelope key": key,
		"passphrase":   []byte(passphrase),
	} {
		if bytes.Contains(protected, forbidden) {
			t.Fatalf("vault file contains %s", name)
		}
	}

	firstProvider.Lock()
	if firstProvider.IsUnlocked() {
		t.Fatal("Lock() left provider unlocked")
	}
	if _, err := secureVault.Decrypt(context.Background(), envelope, []byte("test/passphrase")); err == nil {
		t.Fatal("Decrypt() succeeded after Lock()")
	} else if got := apperrors.Code(err); got != apperrors.VaultLocked {
		t.Fatalf("Decrypt() after Lock() error code = %q, want %q", got, apperrors.VaultLocked)
	}

	secondProvider, err := NewPassphraseKeyProviderWithOptions(path, PassphraseOptions{AllowCreate: false})
	if err != nil {
		t.Fatalf("NewPassphraseKeyProviderWithOptions() after restart error = %v", err)
	}
	if secondProvider.IsUnlocked() {
		t.Fatal("restarted passphrase provider is unlocked before explicit unlock")
	}
	if err := secondProvider.Unlock(context.Background(), "wrong passphrase"); err == nil {
		t.Fatal("Unlock() accepted the wrong passphrase")
	} else if got := apperrors.Code(err); got != apperrors.VaultKeyInvalid {
		t.Fatalf("wrong passphrase error code = %q, want %q", got, apperrors.VaultKeyInvalid)
	}
	if err := secondProvider.Unlock(context.Background(), passphrase); err != nil {
		t.Fatalf("Unlock() after restart error = %v", err)
	}
	secondVault, err := vault.NewEnvelopeVault(secondProvider, vault.EnvelopeOptions{})
	if err != nil {
		t.Fatalf("NewEnvelopeVault() after restart error = %v", err)
	}
	decoded, err := secondVault.Decrypt(context.Background(), envelope, []byte("test/passphrase"))
	if err != nil {
		t.Fatalf("Decrypt() after restart error = %v", err)
	}
	if !bytes.Equal(decoded, key) {
		t.Fatalf("Decrypt() after restart = %x, want %x", decoded, key)
	}
}

func TestPassphraseVaultRejectsTamperedMalformedAndUnsupportedFiles(t *testing.T) {
	path := passphraseTestPath(t)
	provider, err := NewPassphraseKeyProviderWithOptions(path, PassphraseOptions{
		AllowCreate: true,
		Random:      bytes.NewReader(bytes.Repeat([]byte{0x44}, passphraseEnvelopeKeySize+passphraseGenerationSize+passphraseSaltSize+passphraseNonceSize)),
	})
	if err != nil {
		t.Fatalf("NewPassphraseKeyProviderWithOptions() error = %v", err)
	}
	if err := provider.Unlock(context.Background(), "test passphrase"); err != nil {
		t.Fatalf("Unlock() error = %v", err)
	}
	provider.Lock()

	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(vault) error = %v", err)
	}
	original[len(original)-1] ^= 0x01
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("WriteFile(tampered vault) error = %v", err)
	}
	tampered, err := NewPassphraseKeyProviderWithOptions(path, PassphraseOptions{AllowCreate: false})
	if err != nil {
		t.Fatalf("NewPassphraseKeyProviderWithOptions(tampered) error = %v", err)
	}
	if err := tampered.Unlock(context.Background(), "test passphrase"); err == nil {
		t.Fatal("Unlock() accepted tampered protected material")
	} else if got := apperrors.Code(err); got != apperrors.VaultKeyInvalid {
		t.Fatalf("tampered error code = %q, want %q", got, apperrors.VaultKeyInvalid)
	}

	if err := os.WriteFile(path, []byte("malformed passphrase vault"), 0o600); err != nil {
		t.Fatalf("WriteFile(malformed vault) error = %v", err)
	}
	malformed, err := NewPassphraseKeyProviderWithOptions(path, PassphraseOptions{AllowCreate: false})
	if err != nil {
		t.Fatalf("NewPassphraseKeyProviderWithOptions(malformed) error = %v", err)
	}
	if err := malformed.Unlock(context.Background(), "test passphrase"); err == nil {
		t.Fatal("Unlock() accepted malformed protected material")
	} else if got := apperrors.Code(err); got != apperrors.VaultKeyInvalid {
		t.Fatalf("malformed error code = %q, want %q", got, apperrors.VaultKeyInvalid)
	}
}

func TestPassphraseVaultDoesNotRecreateMissingEstablishedMaterial(t *testing.T) {
	path := passphraseTestPath(t)
	provider, err := NewPassphraseKeyProvider(path)
	if err != nil {
		t.Fatalf("NewPassphraseKeyProvider() error = %v", err)
	}
	if err := provider.Unlock(context.Background(), "test passphrase"); err != nil {
		t.Fatalf("Unlock() error = %v", err)
	}
	provider.Lock()
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove(vault) error = %v", err)
	}
	if err := provider.Unlock(context.Background(), "test passphrase"); err == nil {
		t.Fatal("Unlock() recreated missing material after initialization")
	} else if got := apperrors.Code(err); got != apperrors.VaultUnavailable {
		t.Fatalf("missing-after-initialization error code = %q, want %q", got, apperrors.VaultUnavailable)
	}
}

func passphraseTestPath(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("Chmod(test root) error = %v", err)
	}
	return filepath.Join(root, "codex-folio.vault")
}
