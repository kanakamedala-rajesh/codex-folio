//go:build linux

package platform

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

func TestLinuxSecretServiceNativeRoundTripWhenAvailable(t *testing.T) {
	if _, err := exec.LookPath("secret-tool"); err != nil {
		t.Skip("secret-tool is not installed")
	}

	application := fmt.Sprintf("%s.native-test-%d", DefaultSecretServiceApplication, os.Getpid())
	purpose := "envelope-key-native-test-" + strconv.Itoa(os.Getpid())
	options := SecretServiceOptions{
		Application: application,
		Purpose:     purpose,
		Label:       "CodexFolio native test envelope key",
		AllowCreate: true,
	}
	clearSecretServiceTestItem(t, application, purpose)
	t.Cleanup(func() { clearSecretServiceTestItem(t, application, purpose) })

	first, err := NewSecretServiceVaultWithOptions(options)
	if err != nil {
		if code := apperrors.Code(err); code == apperrors.VaultUnavailable || code == apperrors.VaultLocked {
			t.Skipf("Linux Secret Service is not available for this test: %s", code)
		}
		t.Fatalf("NewSecretServiceVaultWithOptions() error = %v", err)
	}
	envelope, err := first.Encrypt(context.Background(), []byte("native Secret Service sentinel"), []byte("native-test/aad"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	secondOptions := options
	secondOptions.AllowCreate = false
	second, err := NewSecretServiceVaultWithOptions(secondOptions)
	if err != nil {
		t.Fatalf("NewSecretServiceVaultWithOptions() after restart error = %v", err)
	}
	plaintext, err := second.Decrypt(context.Background(), envelope, []byte("native-test/aad"))
	if err != nil {
		t.Fatalf("Decrypt() after restart error = %v", err)
	}
	if !bytes.Equal(plaintext, []byte("native Secret Service sentinel")) {
		t.Fatalf("Decrypt() after restart = %q", plaintext)
	}
}

func clearSecretServiceTestItem(t *testing.T, application, purpose string) {
	t.Helper()
	command := exec.Command("secret-tool", "clear", "application", application, "purpose", purpose)
	if err := command.Run(); err != nil {
		// A missing item is expected before the test creates it. Other cleanup
		// errors are intentionally not surfaced because they cannot affect the
		// result of the native round-trip assertion.
		return
	}
}
