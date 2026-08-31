package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

func TestDPAPIProviderRejectsUnsafeVaultPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "relative", path: "vault"},
		{name: "filesystem root", path: string(filepath.Separator)},
		{name: "direct child of filesystem root", path: filepath.Join(string(filepath.Separator), "vault")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewDPAPIKeyProvider(tt.path); err == nil {
				t.Fatal("NewDPAPIKeyProvider() accepted an unsafe path")
			} else if got := apperrors.Code(err); got != apperrors.VaultUnavailable {
				t.Fatalf("error code = %q, want %q", got, apperrors.VaultUnavailable)
			}
		})
	}
}

func TestDPAPIVaultIsUnavailableWithoutWindowsDPAPI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows provides the production DPAPI adapter")
	}

	path := filepath.Join(t.TempDir(), "codex-folio.vault")
	if _, err := NewDPAPIVault(path); err == nil {
		t.Fatal("NewDPAPIVault() succeeded without Windows DPAPI")
	} else if got := apperrors.Code(err); got != apperrors.VaultUnavailable {
		t.Fatalf("error code = %q, want %q", got, apperrors.VaultUnavailable)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("DPAPI-unavailable adapter created %q", path)
	}
}
