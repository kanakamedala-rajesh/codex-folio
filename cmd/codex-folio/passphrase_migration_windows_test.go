//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/buildinfo"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/vault"
)

func TestWindowsPlainStartupMigratesToDPAPIAndImmediatelyLaunches(t *testing.T) {
	paths := windowsMigrationTestPaths(t)
	const passphrase = "one final Windows startup passphrase"
	seedPassphraseMigrationProfile(t, paths, passphrase)
	if err := rememberEverydaySecureStorage(paths, platform.VaultModePassphrase); err != nil {
		t.Fatal(err)
	}

	var fixture *productionCompanionFixture
	starter := func(startPaths platform.Paths, options serviceOptions) error {
		fixture = startMigrationProductionCompanionFixture(startPaths, options, resumeInterruptedPassphraseMigration, migrateOpenedPassphraseStore)
		return nil
	}
	t.Cleanup(func() { fixture.Close() })
	process := &launchTestProcess{pid: 8899, exitStatus: 23}
	var stdout, stderr bytes.Buffer
	code := runWithServicePathResolverAndCodexResolverAndForegroundDependenciesAndCompanionStarter(
		[]string{"--state-root", paths.Root}, strings.NewReader("1\n"+passphrase+"\n\nchild input\n"), &stdout, &stderr, buildinfo.Metadata{},
		func(*string) (platform.Paths, error) { return paths, nil },
		launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "fake-codex.exe"), Version: "0.1.2"}},
		func(platform.Paths, platform.VaultMode, string) (*store.Store, error) {
			return nil, errors.New("plain migrated launch must use the service owner")
		},
		func(launch.Plan, io.Reader, io.Writer, io.Writer) (foregroundProcess, error) { return process, nil }, nil, starter,
	)
	if code != 23 || !process.started {
		t.Fatalf("migrated launch = code:%d started:%t stdout:%q stderr:%q", code, process.started, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Secure-storage migration complete") || strings.Contains(stdout.String(), passphrase) || strings.Contains(stderr.String(), passphrase) {
		t.Fatalf("migration output = stdout:%q stderr:%q", stdout.String(), stderr.String())
	}
	fixture.Close()
	reopened, err := openServiceStore(paths)
	if err != nil {
		t.Fatalf("DPAPI destination did not reopen without a passphrase: %v", err)
	}
	defer reopened.Close()
	if _, err := reopened.GetProfile(context.Background(), "Work"); err != nil {
		t.Fatalf("retained profile missing after plain startup: %v", err)
	}
	resolved, err := resolveEverydaySecureStorage(paths, serviceOptions{})
	if err != nil || resolved.vaultMode == platform.VaultModePassphrase || resolved.migrationTarget != "" {
		t.Fatalf("repeat startup selection = %#v, %v", resolved, err)
	}
}

func TestWindowsPassphraseMigrationReopensRetainedStateWithDPAPI(t *testing.T) {
	paths := windowsMigrationTestPaths(t)
	const passphrase = "one final Windows migration passphrase"
	home, credentialPath := seedPassphraseMigrationProfile(t, paths, passphrase)
	credentialBefore, err := os.ReadFile(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := rememberEverydaySecureStorage(paths, platform.VaultModePassphrase); err != nil {
		t.Fatal(err)
	}

	source, err := openServiceStoreWithVaultMode(paths, platform.VaultModePassphrase, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := migrateOpenedPassphraseStore(paths, platform.VaultModeSecretService, source)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := destination.GetProfile(context.Background(), "Work")
	if err != nil {
		_ = destination.Close()
		t.Fatal(err)
	}
	if retained.Status != profile.StatusReady || retained.IdentityHomePath != home {
		_ = destination.Close()
		t.Fatalf("retained profile = %#v", retained)
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := openServiceStore(paths)
	if err != nil {
		t.Fatalf("DPAPI destination did not reopen without a passphrase: %v", err)
	}
	defer reopened.Close()
	if err := reopened.VerifyProtectedState(context.Background()); err != nil {
		t.Fatalf("DPAPI-protected state failed verification: %v", err)
	}
	credentialAfter, err := os.ReadFile(credentialPath)
	if err != nil || string(credentialAfter) != string(credentialBefore) {
		t.Fatalf("Codex-owned credential changed: %v", err)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("Identity Home changed: %v", err)
	}
	if _, exists, err := readPassphraseMigrationRecord(paths); err != nil || exists {
		t.Fatalf("migration journal after success = exists:%t err:%v", exists, err)
	}
	resolved, err := resolveEverydaySecureStorage(paths, serviceOptions{})
	if err != nil || resolved.vaultMode == platform.VaultModePassphrase {
		t.Fatalf("repeat startup selection = %#v, %v", resolved, err)
	}
}

func TestWindowsPassphraseMigrationResumesMidVaultPromotion(t *testing.T) {
	paths := windowsMigrationTestPaths(t)
	const passphrase = "interrupted Windows migration passphrase"
	seedPassphraseMigrationProfile(t, paths, passphrase)
	source, err := openServiceStoreWithVaultMode(paths, platform.VaultModePassphrase, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := source.CreateVaultMigrationBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := writePassphraseMigrationRecord(paths, passphraseMigrationRecord{
		Target: platform.VaultModeSecretService, BackupID: backup.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := archivePassphraseMigrationBackups(paths); err != nil {
		t.Fatal(err)
	}
	destination, err := newNativeServiceVault(paths, platform.VaultModeSecretService, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.ReprotectVaultState(context.Background(), destination); err != nil {
		t.Fatal(err)
	}
	if err := writePassphraseMigrationRecord(paths, passphraseMigrationRecord{
		Target: platform.VaultModeSecretService, BackupID: backup.ID, DatabaseReady: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(paths.VaultFile, archivedPassphraseVaultPath(paths)); err != nil {
		t.Fatal(err)
	}

	completed, err := resumeInterruptedPassphraseMigration(paths, platform.VaultModeSecretService)
	if err != nil || !completed {
		for cause := err; cause != nil; cause = errors.Unwrap(cause) {
			t.Logf("resume cause: %T: %v", cause, cause)
		}
		t.Fatalf("resume = completed:%t err:%v", completed, err)
	}
	reopened, err := openServiceStore(paths)
	if err != nil {
		t.Fatalf("resumed DPAPI state did not reopen: %v", err)
	}
	defer reopened.Close()
	if _, err := reopened.GetProfile(context.Background(), "Work"); err != nil {
		t.Fatalf("retained profile missing after resume: %v", err)
	}
	if _, err := os.Stat(nativeMigrationVaultPath(paths)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary DPAPI vault remains after resume: %v", err)
	}
}

func TestWindowsPassphraseMigrationDestinationFailurePreservesSource(t *testing.T) {
	paths := windowsMigrationTestPaths(t)
	const passphrase = "recoverable Windows migration passphrase"
	seedPassphraseMigrationProfile(t, paths, passphrase)
	source, err := openServiceStoreWithVaultMode(paths, platform.VaultModePassphrase, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	_, err = migrateOpenedPassphraseStoreWithVaultFactory(paths, platform.VaultModeSecretService, source, func(platform.Paths, platform.VaultMode, bool) (vault.Vault, error) {
		return nil, errors.New("injected DPAPI provider failure")
	})
	if err == nil {
		t.Fatal("migration unexpectedly succeeded")
	}
	reopened, err := openServiceStoreWithVaultMode(paths, platform.VaultModePassphrase, passphrase)
	if err != nil {
		t.Fatalf("passphrase source did not reopen: %v", err)
	}
	defer reopened.Close()
	if _, err := reopened.GetProfile(context.Background(), "Work"); err != nil {
		t.Fatalf("retained profile missing after destination failure: %v", err)
	}
}

func windowsMigrationTestPaths(t *testing.T) platform.Paths {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return platform.Paths{
		Root:              root,
		Runtime:           filepath.Join(root, "runtime"),
		LockFile:          filepath.Join(root, "runtime", "service.owner.lock"),
		MetadataFile:      filepath.Join(root, "runtime", "service.owner.json"),
		DatabaseFile:      filepath.Join(root, "codex-folio.sqlite3"),
		VaultFile:         filepath.Join(root, "codex-folio.vault"),
		WSLVaultFile:      filepath.Join(root, "codex-folio-wsl-dpapi.vault"),
		SecureStorageFile: filepath.Join(root, "secure-storage.json"),
		ManagedHomes:      filepath.Join(root, "managed-homes"),
		ProfileQuarantine: filepath.Join(root, "profile-quarantine"),
	}
}
