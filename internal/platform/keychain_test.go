package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/vault"
)

type fakeKeychainBackend struct {
	record    []byte
	findErr   error
	addErr    error
	deleteErr error
	addCalls  int
}

func (backend *fakeKeychainBackend) find(_, _ string) ([]byte, error) {
	if backend.findErr != nil {
		return nil, backend.findErr
	}
	if backend.record == nil {
		return nil, ErrKeychainItemNotFound
	}
	return bytes.Clone(backend.record), nil
}

func (backend *fakeKeychainBackend) add(_, _ string, record []byte) error {
	backend.addCalls++
	if backend.addErr != nil {
		return backend.addErr
	}
	if backend.record != nil {
		return ErrKeychainItemExists
	}
	backend.record = bytes.Clone(record)
	return nil
}

func (backend *fakeKeychainBackend) delete(_, _ string) error {
	if backend.deleteErr != nil {
		return backend.deleteErr
	}
	backend.record = nil
	return nil
}

func newTestKeychainProvider(t *testing.T, backend keychainBackend, allowCreate bool) *KeychainKeyProvider {
	t.Helper()
	provider, err := newKeychainKeyProviderWithBackend(KeychainOptions{
		Service:     "venkatasudha.com.codex-folio.test",
		Account:     "envelope-key",
		AllowCreate: allowCreate,
		Random:      bytes.NewReader(bytes.Repeat([]byte{0x4a}, keychainGenerationBytes+keychainEnvelopeKeySize)),
	}, backend)
	if err != nil {
		t.Fatalf("newKeychainKeyProviderWithBackend() error = %v", err)
	}
	return provider
}

func TestKeychainProviderCreatesAndReusesOpaqueKeyMaterial(t *testing.T) {
	backend := &fakeKeychainBackend{}
	provider := newTestKeychainProvider(t, backend, true)

	if _, err := provider.LoadOrCreate(context.Background()); err != nil {
		t.Fatalf("LoadOrCreate() first error = %v", err)
	}
	if backend.addCalls != 1 {
		t.Fatalf("keychain add calls = %d, want 1", backend.addCalls)
	}
	if len(backend.record) != keychainRecordSize {
		t.Fatalf("keychain record length = %d, want %d", len(backend.record), keychainRecordSize)
	}

	firstVault, err := vault.NewEnvelopeVault(provider, vault.EnvelopeOptions{})
	if err != nil {
		t.Fatalf("NewEnvelopeVault() first error = %v", err)
	}
	sealed, err := firstVault.Encrypt(context.Background(), []byte("keychain round-trip sentinel"), []byte("test/aad"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	restarted := newTestKeychainProvider(t, backend, true)
	secondVault, err := vault.NewEnvelopeVault(restarted, vault.EnvelopeOptions{})
	if err != nil {
		t.Fatalf("NewEnvelopeVault() after restart error = %v", err)
	}
	opened, err := secondVault.Decrypt(context.Background(), sealed, []byte("test/aad"))
	if err != nil {
		t.Fatalf("Decrypt() after restart error = %v", err)
	}
	if string(opened) != "keychain round-trip sentinel" {
		t.Fatalf("Decrypt() after restart = %q, want sentinel", opened)
	}
}

func TestKeychainProviderFailsClosedForMissingLockedDeniedAndUnavailableStates(t *testing.T) {
	tests := []struct {
		name       string
		backendErr error
		wantCode   string
		wantCause  error
	}{
		{name: "missing", backendErr: ErrKeychainItemNotFound, wantCode: apperrors.VaultUnavailable, wantCause: ErrKeychainItemNotFound},
		{name: "locked", backendErr: ErrKeychainLocked, wantCode: apperrors.VaultLocked, wantCause: ErrKeychainLocked},
		{name: "denied", backendErr: ErrKeychainAccessDenied, wantCode: apperrors.VaultUnavailable, wantCause: ErrKeychainAccessDenied},
		{name: "unavailable", backendErr: ErrKeychainUnavailable, wantCode: apperrors.VaultUnavailable, wantCause: ErrKeychainUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &fakeKeychainBackend{findErr: tt.backendErr}
			provider := newTestKeychainProvider(t, backend, false)
			_, err := provider.LoadOrCreate(context.Background())
			if err == nil {
				t.Fatal("LoadOrCreate() error = nil, want fail-closed error")
			}
			if got := apperrors.Code(err); got != tt.wantCode {
				t.Fatalf("error code = %q, want %q", got, tt.wantCode)
			}
			if !errors.Is(err, tt.wantCause) {
				t.Fatalf("error = %v, want cause %v", err, tt.wantCause)
			}
		})
	}
}

func TestKeychainProviderRedactsUnexpectedBackendErrors(t *testing.T) {
	secret := "keychain backend response must stay redacted"
	backend := &fakeKeychainBackend{findErr: fmt.Errorf("backend response contains %s", secret)}
	provider := newTestKeychainProvider(t, backend, false)

	_, err := provider.LoadOrCreate(context.Background())
	if err == nil {
		t.Fatal("LoadOrCreate() accepted an unavailable keychain")
	}
	if got := apperrors.Code(err); got != apperrors.VaultUnavailable {
		t.Fatalf("error code = %q, want %q", got, apperrors.VaultUnavailable)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error exposed backend response: %v", err)
	}
}

func TestKeychainProviderFailsClosedWhenCreationIsLockedOrDenied(t *testing.T) {
	tests := []struct {
		name       string
		backendErr error
		wantCode   string
		wantCause  error
	}{
		{name: "locked", backendErr: ErrKeychainLocked, wantCode: apperrors.VaultLocked, wantCause: ErrKeychainLocked},
		{name: "denied", backendErr: ErrKeychainAccessDenied, wantCode: apperrors.VaultUnavailable, wantCause: ErrKeychainAccessDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &fakeKeychainBackend{addErr: tt.backendErr}
			provider := newTestKeychainProvider(t, backend, true)
			_, err := provider.LoadOrCreate(context.Background())
			if err == nil {
				t.Fatal("LoadOrCreate() error = nil, want fail-closed error")
			}
			if got := apperrors.Code(err); got != tt.wantCode {
				t.Fatalf("error code = %q, want %q", got, tt.wantCode)
			}
			if !errors.Is(err, tt.wantCause) {
				t.Fatalf("error = %v, want cause %v", err, tt.wantCause)
			}
			if backend.record != nil {
				t.Fatal("failed creation left a Keychain record")
			}
		})
	}
}

func TestKeychainProviderRejectsMalformedMaterial(t *testing.T) {
	backend := &fakeKeychainBackend{record: []byte("malformed keychain record")}
	provider := newTestKeychainProvider(t, backend, false)

	_, err := provider.LoadOrCreate(context.Background())
	if err == nil {
		t.Fatal("LoadOrCreate() accepted malformed keychain material")
	}
	if got := apperrors.Code(err); got != apperrors.VaultKeyInvalid {
		t.Fatalf("error code = %q, want %q", got, apperrors.VaultKeyInvalid)
	}
	if !errors.Is(err, ErrKeychainProtectedMaterial) {
		t.Fatalf("error = %v, want protected-material cause", err)
	}
}

func TestKeychainProviderDoesNotRecreateMissingMaterialAfterInitialization(t *testing.T) {
	backend := &fakeKeychainBackend{}
	provider := newTestKeychainProvider(t, backend, true)
	if _, err := provider.LoadOrCreate(context.Background()); err != nil {
		t.Fatalf("LoadOrCreate() first error = %v", err)
	}
	if err := backend.delete("", ""); err != nil {
		t.Fatalf("delete() error = %v", err)
	}

	if _, err := provider.LoadOrCreate(context.Background()); err == nil {
		t.Fatal("LoadOrCreate() recreated missing material")
	} else if got := apperrors.Code(err); got != apperrors.VaultUnavailable {
		t.Fatalf("missing-after-initialization error code = %q, want %q", got, apperrors.VaultUnavailable)
	}
	if backend.addCalls != 1 {
		t.Fatalf("keychain add calls = %d, want no recreation after initialization", backend.addCalls)
	}
}

func TestKeychainProviderMapsMalformedStoredRecordToInvalidKey(t *testing.T) {
	backend := &fakeKeychainBackend{record: []byte{keychainRecordMagic[0], keychainRecordVersion}}
	provider := newTestKeychainProvider(t, backend, false)

	_, err := provider.LoadOrCreate(context.Background())
	if err == nil {
		t.Fatal("LoadOrCreate() accepted truncated keychain material")
	}
	if got := apperrors.Code(err); got != apperrors.VaultKeyInvalid {
		t.Fatalf("error code = %q, want %q", got, apperrors.VaultKeyInvalid)
	}
}

func TestKeychainProviderRejectsInvalidAttributes(t *testing.T) {
	tooLong := strings.Repeat("x", maxKeychainAttributeSize+1)
	tests := []KeychainOptions{
		{Service: "\x00service"},
		{Service: tooLong},
		{Service: "service", Account: "\x00account"},
		{Service: "service", Account: tooLong},
	}
	for index, options := range tests {
		if _, err := NewKeychainKeyProviderWithOptions(options); err == nil {
			t.Fatalf("case %d: NewKeychainKeyProviderWithOptions() accepted invalid attributes", index)
		} else if got := apperrors.Code(err); got != apperrors.VaultUnavailable {
			t.Fatalf("case %d: error code = %q, want %q", index, got, apperrors.VaultUnavailable)
		}
	}
}

func TestKeychainVaultIsUnavailableWithoutMacOSKeychain(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS provides the production Keychain adapter")
	}

	if _, err := NewKeychainVault(); err == nil {
		t.Fatal("NewKeychainVault() succeeded without macOS Keychain")
	} else if got := apperrors.Code(err); got != apperrors.VaultUnavailable {
		t.Fatalf("error code = %q, want %q", got, apperrors.VaultUnavailable)
	}
}
