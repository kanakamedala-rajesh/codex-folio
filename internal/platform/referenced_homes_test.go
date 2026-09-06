package platform

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReferencedHomeResolverReturnsCanonicalExistingDirectory(t *testing.T) {
	home := t.TempDir()
	resolver := NewReferencedHomeResolver()

	resolved, err := resolver.Resolve(context.Background(), home)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	if resolved != filepath.Clean(want) {
		t.Fatalf("resolved home = %q, want %q", resolved, filepath.Clean(want))
	}
}

func TestReferencedHomeResolverRejectsInvalidHomes(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "not-a-home")
	if err := os.WriteFile(file, []byte("not a home"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	resolver := NewReferencedHomeResolver()

	for _, home := range []string{"relative-home", filepath.Join(root, "missing"), file} {
		if _, err := resolver.Resolve(context.Background(), home); err == nil {
			t.Errorf("Resolve(%q) succeeded, want invalid-home error", home)
		}
	}
}

func TestReferencedHomeResolverRejectsManagedBoundaryAndSymlink(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed-homes")
	if err := os.MkdirAll(filepath.Join(managed, "profile"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	link := filepath.Join(root, "managed-link")
	if err := os.Symlink(managed, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	resolver := NewReferencedHomeResolver(managed)
	for _, home := range []string{filepath.Join(managed, "profile"), filepath.Join(link, "profile")} {
		if _, err := resolver.Resolve(context.Background(), home); err == nil {
			t.Errorf("Resolve(%q) succeeded, want managed-home rejection", home)
		}
	}
}

func TestReferencedHomeResolverRejectsQuarantineBoundary(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed-homes")
	quarantine := filepath.Join(root, "profile-quarantine")
	home := filepath.Join(quarantine, "profile-1")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewReferencedHomeResolver(managed, quarantine).Resolve(context.Background(), home); err == nil {
		t.Fatal("Resolve() succeeded for quarantined managed home")
	}
}
