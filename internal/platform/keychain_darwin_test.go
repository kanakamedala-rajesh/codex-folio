//go:build darwin && cgo

package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/platform/keychaintest"
	"venkatasudha.com/codex-folio/internal/vault"
)

func TestMain(m *testing.M) {
	if err := keychaintest.ConfigureFromEnvironment("CODEX_FOLIO_PLATFORM_KEYCHAIN_PATH"); err != nil {
		fmt.Fprintf(os.Stderr, "configure native platform test Keychain: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

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

func TestKeychainProviderFailsClosedForNativeLockedAndDeniedStates(t *testing.T) {
	service := fmt.Sprintf("%s.states.%d", DefaultKeychainService, os.Getpid())
	account := fmt.Sprintf("%s.%d", DefaultKeychainAccount, os.Getpid())
	backend := newSystemKeychainBackend()
	if err := backend.delete(service, account); err != nil {
		t.Fatalf("delete stale test item: %v", err)
	}
	t.Cleanup(func() {
		if err := keychaintest.SetItemTrustAll(service, account, true); err != nil {
			t.Errorf("restore test item access: %v", err)
		}
		if err := keychaintest.Delete(service, account); err != nil {
			t.Errorf("delete test item: %v", err)
		}
	})

	options := KeychainOptions{Service: service, Account: account, AllowCreate: true}
	provider, err := NewKeychainKeyProviderWithOptions(options)
	if err != nil {
		t.Fatalf("NewKeychainKeyProviderWithOptions() error = %v", err)
	}
	if _, err := provider.LoadOrCreate(context.Background()); err != nil {
		t.Fatalf("LoadOrCreate() setup error = %v", err)
	}
	keychainRecord, err := keychaintest.Read(service, account)
	if err != nil {
		t.Fatalf("read native test item: %v", err)
	}
	defer clear(keychainRecord)

	t.Run("locked", func(t *testing.T) {
		if err := keychaintest.LockDefault(); err != nil {
			t.Fatalf("lock default Keychain: %v", err)
		}
		t.Cleanup(func() {
			if err := keychaintest.UnlockDefault(os.Getenv("CODEX_FOLIO_TEST_KEYCHAIN_PASSWORD")); err != nil {
				t.Errorf("unlock default Keychain: %v", err)
			}
		})

		_, err := provider.LoadOrCreate(context.Background())
		if err == nil || apperrors.Code(err) != apperrors.VaultLocked || !errors.Is(err, vault.ErrLocked) {
			t.Fatalf("locked LoadOrCreate() error = %v, want VaultLocked", err)
		}
		if bytes.Contains([]byte(err.Error()), keychainRecord) {
			t.Fatal("locked error exposes Keychain material")
		}
	})

	if err := keychaintest.SetItemTrustAll(service, account, false); err != nil {
		t.Fatalf("deny test item access: %v", err)
	}
	_, err = provider.LoadOrCreate(context.Background())
	if err == nil || apperrors.Code(err) != apperrors.VaultUnavailable || !errors.Is(err, ErrKeychainAccessDenied) {
		t.Fatalf("denied LoadOrCreate() error = %v, want VaultUnavailable/access denied", err)
	}
	if bytes.Contains([]byte(err.Error()), keychainRecord) {
		t.Fatal("denied error exposes Keychain material")
	}
}
