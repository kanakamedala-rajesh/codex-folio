//go:build darwin && cgo

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/platform/keychaintest"
	"venkatasudha.com/codex-folio/internal/store"
)

func TestDarwinServiceStoreReusesKeychainForEncryptedProjectIdentity(t *testing.T) {
	root := testServiceTempDir(t)
	override := root
	paths, err := platform.ResolvePaths(platform.PathOptions{
		Platform:          platform.PlatformDarwin,
		HomeDir:           filepath.Join(root, "home"),
		OwnerHomeDir:      filepath.Join(root, "owner-home"),
		StateRootOverride: &override,
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}

	service := fmt.Sprintf("%s.test.%d", platform.DefaultKeychainService, os.Getpid())
	account := fmt.Sprintf("%s.test.%d", platform.DefaultKeychainAccount, os.Getpid())
	if err := keychaintest.Delete(service, account); err != nil {
		t.Fatalf("delete stale test item: %v", err)
	}
	t.Cleanup(func() {
		if err := keychaintest.Delete(service, account); err != nil {
			t.Errorf("delete test item: %v", err)
		}
	})

	keychainOptions := platform.KeychainOptions{
		Service:     service,
		Account:     account,
		AllowCreate: true,
	}
	project := store.ProjectIdentity{
		ProjectIdentityID: "project-1",
		ProjectAlias:      "example",
		CanonicalPath:     "/private/sentinel/project",
		CreatedAt:         time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, time.August, 31, 12, 1, 0, 0, time.UTC),
	}

	firstVault, err := platform.NewKeychainVaultWithOptions(keychainOptions)
	if err != nil {
		t.Fatalf("NewKeychainVaultWithOptions() first error = %v", err)
	}
	first, err := store.OpenWithVault(paths.DatabaseFile, firstVault)
	if err != nil {
		t.Fatalf("OpenWithVault() first error = %v", err)
	}
	if err := first.PutProjectIdentity(context.Background(), project); err != nil {
		_ = first.Close()
		t.Fatalf("PutProjectIdentity() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Store.Close() error = %v", err)
	}

	secondVault, err := platform.NewKeychainVaultWithOptions(keychainOptions)
	if err != nil {
		t.Fatalf("NewKeychainVaultWithOptions() after restart error = %v", err)
	}
	second, err := store.OpenWithVault(paths.DatabaseFile, secondVault)
	if err != nil {
		t.Fatalf("OpenWithVault() after restart error = %v", err)
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

	keychainRecord, err := keychaintest.Read(service, account)
	if err != nil {
		t.Fatalf("find Keychain record: %v", err)
	}
	defer clear(keychainRecord)
	if len(keychainRecord) < 32 {
		t.Fatalf("Keychain record length = %d, want key material", len(keychainRecord))
	}
	keyMaterial := keychainRecord[len(keychainRecord)-32:]

	databaseBytes, err := os.ReadFile(paths.DatabaseFile)
	if err != nil {
		t.Fatalf("ReadFile(database): %v", err)
	}
	for name, forbidden := range map[string][]byte{
		"canonical project path": []byte(project.CanonicalPath),
		"Keychain record":        keychainRecord,
		"Keychain key material":  keyMaterial,
	} {
		if bytes.Contains(databaseBytes, forbidden) {
			t.Fatalf("database contains %s", name)
		}
	}
}
