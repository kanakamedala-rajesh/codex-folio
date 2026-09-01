//go:build darwin && cgo

package platform

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

func TestKeychainVaultRoundTripsAndReusesKeyAcrossProviderRestart(t *testing.T) {
	service := fmt.Sprintf("%s.test.%d", DefaultKeychainService, os.Getpid())
	account := fmt.Sprintf("%s.%d", DefaultKeychainAccount, os.Getpid())
	backend := newSystemKeychainBackend()
	if err := backend.delete(service, account); err != nil {
		t.Fatalf("delete stale test item: %v", err)
	}
	t.Cleanup(func() {
		if err := backend.delete(service, account); err != nil {
			t.Errorf("delete test item: %v", err)
		}
	})

	options := KeychainOptions{Service: service, Account: account, AllowCreate: true}
	first, err := NewKeychainVaultWithOptions(options)
	if err != nil {
		t.Fatalf("NewKeychainVaultWithOptions() first error = %v", err)
	}

	plaintext := []byte("macOS keychain envelope sentinel")
	associatedData := []byte("codex-folio/project-identities/project-1/canonical-path")
	sealed, err := first.Encrypt(context.Background(), plaintext, associatedData)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	record, err := backend.find(service, account)
	if err != nil {
		t.Fatalf("find keychain record: %v", err)
	}
	if bytes.Contains(record, plaintext) {
		t.Fatal("Keychain record contains the envelope plaintext")
	}
	defer clear(record)

	second, err := NewKeychainVaultWithOptions(options)
	if err != nil {
		t.Fatalf("NewKeychainVaultWithOptions() after restart error = %v", err)
	}
	opened, err := second.Decrypt(context.Background(), sealed, associatedData)
	if err != nil {
		t.Fatalf("Decrypt() after restart error = %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("Decrypt() after restart = %q, want %q", opened, plaintext)
	}
}

func TestKeychainProviderDoesNotRecreateMissingItemAfterInitialization(t *testing.T) {
	service := fmt.Sprintf("%s.missing.%d", DefaultKeychainService, os.Getpid())
	account := fmt.Sprintf("%s.missing.%d", DefaultKeychainAccount, os.Getpid())
	backend := newSystemKeychainBackend()
	if err := backend.delete(service, account); err != nil {
		t.Fatalf("delete stale test item: %v", err)
	}
	t.Cleanup(func() {
		if err := backend.delete(service, account); err != nil {
			t.Errorf("delete test item: %v", err)
		}
	})

	provider, err := newKeychainKeyProviderWithBackend(KeychainOptions{
		Service:     service,
		Account:     account,
		AllowCreate: true,
	}, backend)
	if err != nil {
		t.Fatalf("newKeychainKeyProviderWithBackend() error = %v", err)
	}
	if _, err := provider.LoadOrCreate(context.Background()); err != nil {
		t.Fatalf("LoadOrCreate() first error = %v", err)
	}
	if err := backend.delete(service, account); err != nil {
		t.Fatalf("delete initialized item: %v", err)
	}
	if _, err := provider.LoadOrCreate(context.Background()); err == nil {
		t.Fatal("LoadOrCreate() recreated missing Keychain item")
	} else if got := apperrors.Code(err); got != apperrors.VaultUnavailable {
		t.Fatalf("missing-after-initialization error code = %q, want %q", got, apperrors.VaultUnavailable)
	}
}
