package platform

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

func TestManagedHomeProvisionerCreatesPrivateStableHome(t *testing.T) {
	root := filepath.Join(testTempDir(t), "managed-homes")
	provisioner, err := NewManagedHomeProvisioner(root)
	if err != nil {
		t.Fatalf("NewManagedHomeProvisioner() error = %v", err)
	}
	first, err := provisioner.Ensure(context.Background(), "profile-1")
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	second, err := provisioner.Ensure(context.Background(), "profile-1")
	if err != nil {
		t.Fatalf("second Ensure() error = %v", err)
	}
	if first != filepath.Join(root, "profile-1") || second != first {
		t.Fatalf("home paths = %q and %q, want one stable child below %q", first, second, root)
	}
	for _, path := range []string{root, first} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("Stat(%q) error = %v", path, statErr)
		}
		if !info.IsDir() {
			t.Fatalf("%q is not a directory", path)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
			t.Fatalf("%q mode = %o, want 700", path, info.Mode().Perm())
		}
	}
}

func TestManagedHomeProvisionerRejectsUnsafeProfileIdentifiers(t *testing.T) {
	provisioner, err := NewManagedHomeProvisioner(filepath.Join(testTempDir(t), "managed-homes"))
	if err != nil {
		t.Fatalf("NewManagedHomeProvisioner() error = %v", err)
	}
	for _, profileID := range []string{"", ".", "..", "../other", `..\other`} {
		if _, err := provisioner.Ensure(context.Background(), profileID); err == nil || apperrors.Code(err) != apperrors.PlatformStatePathUnsafe {
			t.Fatalf("Ensure(%q) error = %v, want unsafe-path error", profileID, err)
		}
	}
}
