//go:build windows

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

func TestWindowsServiceStoreReusesDPAPIForEncryptedFields(t *testing.T) {
	root := t.TempDir()
	override := root
	paths, err := platform.ResolvePaths(platform.PathOptions{
		Platform:          platform.PlatformWindows,
		HomeDir:           filepath.Join(root, "home"),
		OwnerHomeDir:      filepath.Join(root, "owner-home"),
		StateRootOverride: &override,
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}

	project := store.ProjectIdentity{
		ProjectIdentityID: "project-1",
		ProjectAlias:      "example",
		CanonicalPath:     `C:\Users\alice\private\project`,
		CreatedAt:         time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, time.August, 31, 12, 1, 0, 0, time.UTC),
	}

	first, err := openServiceStore(paths)
	if err != nil {
		t.Fatalf("openServiceStore() first error = %v", err)
	}
	if err := first.PutProjectIdentity(context.Background(), project); err != nil {
		_ = first.Close()
		t.Fatalf("PutProjectIdentity() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Store.Close() error = %v", err)
	}

	second, err := openServiceStore(paths)
	if err != nil {
		t.Fatalf("openServiceStore() after restart error = %v", err)
	}
	got, err := second.GetProjectIdentity(context.Background(), project.ProjectIdentityID)
	if err != nil {
		_ = second.Close()
		t.Fatalf("GetProjectIdentity() after restart error = %v", err)
	}
	if got != project {
		_ = second.Close()
		t.Fatalf("GetProjectIdentity() after restart = %#v, want %#v", got, project)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Store.Close() error = %v", err)
	}

	databaseBytes, err := os.ReadFile(paths.DatabaseFile)
	if err != nil {
		t.Fatalf("ReadFile(database): %v", err)
	}
	protectedVaultMaterial, err := os.ReadFile(paths.VaultFile)
	if err != nil {
		t.Fatalf("ReadFile(vault): %v", err)
	}
	if bytes.Contains(databaseBytes, []byte(project.CanonicalPath)) {
		t.Fatal("database contains canonical project path in plaintext")
	}
	if bytes.Contains(databaseBytes, protectedVaultMaterial) {
		t.Fatal("database contains protected vault material")
	}
}

func TestWindowsServiceStoreFailsClosedWhenVaultDisappearsFromEstablishedState(t *testing.T) {
	root := t.TempDir()
	override := root
	paths, err := platform.ResolvePaths(platform.PathOptions{
		Platform:          platform.PlatformWindows,
		HomeDir:           filepath.Join(root, "home"),
		OwnerHomeDir:      filepath.Join(root, "owner-home"),
		StateRootOverride: &override,
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}

	first, err := openServiceStore(paths)
	if err != nil {
		t.Fatalf("openServiceStore() first error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Store.Close() error = %v", err)
	}
	if err := os.Remove(paths.VaultFile); err != nil {
		t.Fatalf("Remove(vault): %v", err)
	}

	if _, err := openServiceStore(paths); err == nil {
		t.Fatal("openServiceStore() recreated missing vault material")
	} else if got := apperrors.Code(err); got != apperrors.VaultUnavailable {
		t.Fatalf("missing vault error code = %q, want %q", got, apperrors.VaultUnavailable)
	}
}
