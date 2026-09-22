//go:build linux

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

func TestLinuxServiceStoreUsesPassphraseVaultForEncryptedFields(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("Chmod(state root) error = %v", err)
	}
	override := root
	paths, err := platform.ResolvePaths(platform.PathOptions{
		Platform:          platform.PlatformLinux,
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
		CanonicalPath:     "/private/passphrase-vault/project",
		CreatedAt:         time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, time.August, 31, 12, 1, 0, 0, time.UTC),
	}
	const passphrase = "correct horse battery staple"

	first, err := openServiceStoreWithVaultMode(paths, platform.VaultModePassphrase, passphrase)
	if err != nil {
		t.Fatalf("openServiceStoreWithVaultMode() first error = %v", err)
	}
	if err := first.PutProjectIdentity(context.Background(), project); err != nil {
		_ = first.Close()
		t.Fatalf("PutProjectIdentity() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Store.Close() error = %v", err)
	}

	second, err := openServiceStoreWithVaultMode(paths, platform.VaultModePassphrase, passphrase)
	if err != nil {
		t.Fatalf("openServiceStoreWithVaultMode() after restart error = %v", err)
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
		t.Fatalf("ReadFile(database) error = %v", err)
	}
	if bytes.Contains(databaseBytes, []byte(project.CanonicalPath)) {
		t.Fatal("database contains canonical project path in plaintext")
	}
	vaultBytes, err := os.ReadFile(paths.VaultFile)
	if err != nil {
		t.Fatalf("ReadFile(vault) error = %v", err)
	}
	if bytes.Contains(vaultBytes, []byte(passphrase)) {
		t.Fatal("passphrase vault file contains the passphrase")
	}
	if info, err := os.Stat(paths.VaultFile); err != nil {
		t.Fatalf("Stat(vault) error = %v", err)
	} else if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("vault file permissions = %o, want 600", got)
	}
}

func TestWSLServiceStoreReopensThroughWindowsUserDPAPI(t *testing.T) {
	if os.Getenv("CODEX_FOLIO_WSL_VAULT_NATIVE_TEST") != "1" {
		t.Skip("set CODEX_FOLIO_WSL_VAULT_NATIVE_TEST=1 for native WSL service composition")
	}
	if !platform.IsWSL2() {
		t.Skip("native WSL service composition requires WSL2")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	override := root
	paths, err := platform.ResolvePaths(platform.PathOptions{Platform: platform.PlatformLinux, HomeDir: filepath.Join(root, "home"), OwnerHomeDir: filepath.Join(root, "owner"), StateRootOverride: &override})
	if err != nil {
		t.Fatal(err)
	}
	project := store.ProjectIdentity{ProjectIdentityID: "wsl-project", ProjectAlias: "wsl", CanonicalPath: "/private/wsl/project", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	first, err := openServiceStoreWithVaultMode(paths, platform.VaultModeWSLDPAPI, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.PutProjectIdentity(context.Background(), project); err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := openServiceStoreWithVaultMode(paths, platform.VaultModeWSLDPAPI, "")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	got, err := second.GetProjectIdentity(context.Background(), project.ProjectIdentityID)
	if err != nil || got.CanonicalPath != project.CanonicalPath {
		t.Fatalf("reopened project = %#v, %v", got, err)
	}
	if _, err := os.Stat(paths.WSLVaultFile); err != nil {
		t.Fatalf("WSL vault file: %v", err)
	}
	if _, err := os.Stat(paths.VaultFile); !os.IsNotExist(err) {
		t.Fatalf("legacy vault file unexpectedly used: %v", err)
	}
}
