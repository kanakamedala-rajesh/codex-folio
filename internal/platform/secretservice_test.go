package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/vault"
)

type fakeSecretServiceBackend struct {
	record     []byte
	lookupErr  error
	storeErr   error
	storeCalls int
}

func (backend *fakeSecretServiceBackend) lookup(context.Context, map[string]string) ([]byte, error) {
	if backend.lookupErr != nil {
		return nil, backend.lookupErr
	}
	if backend.record == nil {
		return nil, ErrSecretServiceItemNotFound
	}
	return bytes.Clone(backend.record), nil
}

func (backend *fakeSecretServiceBackend) store(_ context.Context, _ string, _ map[string]string, record []byte) error {
	backend.storeCalls++
	if backend.storeErr != nil {
		return backend.storeErr
	}
	if backend.record != nil {
		return ErrSecretServiceItemExists
	}
	backend.record = bytes.Clone(record)
	return nil
}

func newTestSecretServiceProvider(t *testing.T, backend secretServiceBackend, allowCreate bool) *SecretServiceKeyProvider {
	t.Helper()
	provider, err := newSecretServiceKeyProviderWithBackend(SecretServiceOptions{
		Application: "venkatasudha.com.codex-folio.test",
		Purpose:     "envelope-key",
		Label:       "CodexFolio test envelope key",
		AllowCreate: allowCreate,
		Random:      bytes.NewReader(bytes.Repeat([]byte{0x4a}, secretServiceGenerationBytes+secretServiceEnvelopeKeySize)),
	}, backend)
	if err != nil {
		t.Fatalf("newSecretServiceKeyProviderWithBackend() error = %v", err)
	}
	return provider
}

func TestSecretServiceProviderCreatesAndReusesOpaqueKeyMaterial(t *testing.T) {
	backend := &fakeSecretServiceBackend{}
	provider := newTestSecretServiceProvider(t, backend, true)
	if _, err := provider.LoadOrCreate(context.Background()); err != nil {
		t.Fatalf("LoadOrCreate() first error = %v", err)
	}
	if backend.storeCalls != 1 {
		t.Fatalf("Secret Service store calls = %d, want 1", backend.storeCalls)
	}
	if len(backend.record) != secretServiceRecordSize {
		t.Fatalf("Secret Service record length = %d, want %d", len(backend.record), secretServiceRecordSize)
	}

	firstVault, err := vault.NewEnvelopeVault(provider, vault.EnvelopeOptions{})
	if err != nil {
		t.Fatalf("NewEnvelopeVault() first error = %v", err)
	}
	envelope, err := firstVault.Encrypt(context.Background(), []byte("Secret Service round-trip sentinel"), []byte("test/aad"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	restarted := newTestSecretServiceProvider(t, backend, true)
	secondVault, err := vault.NewEnvelopeVault(restarted, vault.EnvelopeOptions{})
	if err != nil {
		t.Fatalf("NewEnvelopeVault() after restart error = %v", err)
	}
	decoded, err := secondVault.Decrypt(context.Background(), envelope, []byte("test/aad"))
	if err != nil {
		t.Fatalf("Decrypt() after restart error = %v", err)
	}
	if string(decoded) != "Secret Service round-trip sentinel" {
		t.Fatalf("Decrypt() after restart = %q", decoded)
	}
}

func TestSecretServiceProviderFailsClosedForMissingLockedDeniedAndUnavailable(t *testing.T) {
	tests := []struct {
		name       string
		backendErr error
		wantCode   string
		wantCause  error
	}{
		{name: "missing", backendErr: ErrSecretServiceItemNotFound, wantCode: apperrors.VaultUnavailable, wantCause: ErrSecretServiceItemNotFound},
		{name: "locked", backendErr: ErrSecretServiceLocked, wantCode: apperrors.VaultLocked, wantCause: ErrSecretServiceLocked},
		{name: "denied", backendErr: ErrSecretServiceAccessDenied, wantCode: apperrors.VaultUnavailable, wantCause: ErrSecretServiceAccessDenied},
		{name: "unavailable", backendErr: ErrSecretServiceUnavailable, wantCode: apperrors.VaultUnavailable, wantCause: ErrSecretServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := newTestSecretServiceProvider(t, &fakeSecretServiceBackend{lookupErr: tt.backendErr}, false)
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

func TestSecretServiceProviderRejectsMalformedAndRedactsUnexpectedErrors(t *testing.T) {
	provider := newTestSecretServiceProvider(t, &fakeSecretServiceBackend{record: []byte("malformed Secret Service record")}, false)
	if _, err := provider.LoadOrCreate(context.Background()); err == nil {
		t.Fatal("LoadOrCreate() accepted malformed Secret Service material")
	} else if got := apperrors.Code(err); got != apperrors.VaultKeyInvalid {
		t.Fatalf("malformed error code = %q, want %q", got, apperrors.VaultKeyInvalid)
	}

	secret := "Secret Service backend detail sentinel"
	provider = newTestSecretServiceProvider(t, &fakeSecretServiceBackend{lookupErr: fmt.Errorf("backend contains %s", secret)}, false)
	if _, err := provider.LoadOrCreate(context.Background()); err == nil {
		t.Fatal("LoadOrCreate() accepted unexpected Secret Service error")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatalf("error exposed backend detail: %v", err)
	}
}

func TestSecretServiceProviderRejectsInvalidAttributes(t *testing.T) {
	tooLong := strings.Repeat("x", maxSecretServiceAttributeSize+1)
	for index, options := range []SecretServiceOptions{
		{Application: "\x00application"},
		{Application: "-application"},
		{Application: tooLong},
		{Purpose: "\x00purpose"},
		{Label: tooLong},
	} {
		if _, err := newSecretServiceKeyProviderWithBackend(options, &fakeSecretServiceBackend{}); err == nil {
			t.Fatalf("case %d: constructor accepted invalid Secret Service attributes", index)
		} else if got := apperrors.Code(err); got != apperrors.VaultUnavailable {
			t.Fatalf("case %d: error code = %q, want %q", index, got, apperrors.VaultUnavailable)
		}
	}
}

func TestSecretServiceProviderDoesNotRecreateMissingMaterialAfterInitialization(t *testing.T) {
	backend := &fakeSecretServiceBackend{}
	provider := newTestSecretServiceProvider(t, backend, true)
	if _, err := provider.LoadOrCreate(context.Background()); err != nil {
		t.Fatalf("LoadOrCreate() first error = %v", err)
	}
	backend.record = nil
	if _, err := provider.LoadOrCreate(context.Background()); err == nil {
		t.Fatal("LoadOrCreate() recreated missing Secret Service material")
	} else if got := apperrors.Code(err); got != apperrors.VaultUnavailable {
		t.Fatalf("missing-after-initialization error code = %q, want %q", got, apperrors.VaultUnavailable)
	}
	if backend.storeCalls != 1 {
		t.Fatalf("Secret Service store calls = %d, want 1", backend.storeCalls)
	}
}

func TestSecretServiceVaultIsUnavailableWithoutLinuxSecretService(t *testing.T) {
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("secret-tool"); err == nil {
			t.Skip("secret-tool is installed; native availability is covered by the Linux integration test")
		}
	}
	if _, err := NewSecretServiceVault(); err == nil {
		t.Fatal("NewSecretServiceVault() succeeded without Linux Secret Service")
	} else if got := apperrors.Code(err); got != apperrors.VaultUnavailable {
		t.Fatalf("error code = %q, want %q", got, apperrors.VaultUnavailable)
	}
}
