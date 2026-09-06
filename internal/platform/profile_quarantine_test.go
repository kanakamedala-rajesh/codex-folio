package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProfileHomeLifecycleQuarantinesRestoresAndPurgesOnlyManagedHome(t *testing.T) {
	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed-homes")
	quarantineRoot := filepath.Join(root, "profile-quarantine")
	home := filepath.Join(managedRoot, "profile-1")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	marker := filepath.Join(home, "auth.json")
	if err := os.WriteFile(marker, []byte("opaque"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	lifecycle, err := NewProfileHomeLifecycle(managedRoot, quarantineRoot)
	if err != nil {
		t.Fatalf("NewProfileHomeLifecycle() error = %v", err)
	}
	if err := lifecycle.Quarantine(context.Background(), "profile-1", home); err != nil {
		t.Fatalf("Quarantine() error = %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("managed marker Stat() error = %v, want not exist", err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("MkdirAll(collision) error = %v", err)
	}
	if err := lifecycle.Restore(context.Background(), "profile-1", home); err == nil {
		t.Fatal("Restore() collision error = nil")
	}
	if err := os.Remove(home); err != nil {
		t.Fatalf("Remove(collision) error = %v", err)
	}
	if err := lifecycle.Restore(context.Background(), "profile-1", home); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "opaque" {
		t.Fatalf("restored marker = %q, error = %v", data, err)
	}
	if err := lifecycle.Quarantine(context.Background(), "profile-1", home); err != nil {
		t.Fatalf("second Quarantine() error = %v", err)
	}
	if err := lifecycle.Purge(context.Background(), "profile-1"); err != nil {
		t.Fatalf("Purge() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(quarantineRoot, "profile-1")); !os.IsNotExist(err) {
		t.Fatalf("quarantine Stat() error = %v, want not exist", err)
	}
}

func TestProfileHomeLifecycleLeavesManagedHomeInPlaceWhenMoveFails(t *testing.T) {
	root := t.TempDir()
	managedRoot := filepath.Join(root, "managed-homes")
	home := filepath.Join(managedRoot, "profile-1")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	injected := errors.New("permission denied")
	filesystem := failingProfileHomeFileSystem{renameErr: injected}
	lifecycle, err := NewProfileHomeLifecycleWithFileSystem(managedRoot, filepath.Join(root, "profile-quarantine"), filesystem)
	if err != nil {
		t.Fatalf("NewProfileHomeLifecycleWithFileSystem() error = %v", err)
	}
	if err := lifecycle.Quarantine(context.Background(), "profile-1", home); !errors.Is(err, injected) {
		t.Fatalf("Quarantine() error = %v, want injected permission failure", err)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("managed home changed after failed move: %v", err)
	}
}

type failingProfileHomeFileSystem struct{ renameErr error }

func (failingProfileHomeFileSystem) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }
func (failingProfileHomeFileSystem) MkdirAll(path string, mode os.FileMode) error {
	return os.MkdirAll(path, mode)
}
func (filesystem failingProfileHomeFileSystem) Rename(string, string) error {
	return filesystem.renameErr
}
func (failingProfileHomeFileSystem) RemoveAll(path string) error { return os.RemoveAll(path) }
