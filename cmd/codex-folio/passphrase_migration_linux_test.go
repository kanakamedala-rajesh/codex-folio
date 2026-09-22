//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/buildinfo"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/vault"
)

func TestPlainStartupMigratesPassphraseStateAndImmediatelyLaunches(t *testing.T) {
	paths := launchTestPaths(t)
	const passphrase = "one final private passphrase"
	home, credentialPath := seedPassphraseMigrationProfile(t, paths, passphrase)
	credentialBefore, err := os.ReadFile(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := rememberEverydaySecureStorage(paths, platform.VaultModePassphrase); err != nil {
		t.Fatal(err)
	}

	destinationKey := bytes.Repeat([]byte{0x6d}, 32)
	newDestination := func(_ platform.Paths, _ platform.VaultMode, _ bool) (vault.Vault, error) {
		return vault.NewInMemoryVault(destinationKey, "native-migration-generation")
	}
	migrateCalls := 0
	migrator := func(migrationPaths platform.Paths, target platform.VaultMode, sourceStore *store.Store) (*store.Store, error) {
		migrateCalls++
		return migrateOpenedPassphraseStoreWithVaultFactory(migrationPaths, target, sourceStore, newDestination)
	}
	resumer := func(resumePaths platform.Paths, target platform.VaultMode) (bool, error) {
		return resumeInterruptedPassphraseMigrationWithVaultFactory(resumePaths, target, newDestination)
	}

	var fixture *productionCompanionFixture
	starter := func(startPaths platform.Paths, options serviceOptions) error {
		if options.vaultMode != platform.VaultModePassphrase || (options.migrationTarget != platform.VaultModeSecretService && options.migrationTarget != platform.VaultModeWSLDPAPI) {
			return errors.New("migrated companion did not select native storage")
		}
		fixture = startMigrationProductionCompanionFixture(startPaths, options, resumer, migrator)
		return nil
	}
	t.Cleanup(func() { fixture.Close() })
	process := &launchTestProcess{pid: 8808, exitStatus: 23}
	var stdout, stderr bytes.Buffer
	code := runWithServicePathResolverAndCodexResolverAndForegroundDependenciesAndCompanionStarter(
		[]string{"--state-root", paths.Root}, strings.NewReader("1\n"+passphrase+"\n\nchild input\n"), &stdout, &stderr, buildinfo.Metadata{},
		func(*string) (platform.Paths, error) { return paths, nil },
		launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "fake-codex"), Version: "0.1.2"}},
		func(platform.Paths, platform.VaultMode, string) (*store.Store, error) {
			return nil, errors.New("plain migrated launch must use the service owner")
		},
		func(launch.Plan, io.Reader, io.Writer, io.Writer) (foregroundProcess, error) { return process, nil }, nil, starter,
	)
	if code != 23 || migrateCalls != 1 || !process.started {
		t.Fatalf("migrated launch = code:%d migrations:%d started:%t stdout:%q stderr:%q", code, migrateCalls, process.started, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Secure-storage migration complete") || !strings.Contains(stdout.String(), "dashboard address:") || strings.Contains(stdout.String(), passphrase) || strings.Contains(stderr.String(), passphrase) {
		t.Fatalf("migration output = stdout:%q stderr:%q", stdout.String(), stderr.String())
	}
	credentialAfter, err := os.ReadFile(credentialPath)
	if err != nil || !bytes.Equal(credentialBefore, credentialAfter) {
		t.Fatalf("Codex-owned credential fixture changed: %v", err)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("Identity Home changed: %v", err)
	}
	selection, err := os.ReadFile(paths.SecureStorageFile)
	if err != nil || bytes.Contains(selection, []byte(`"mode":"passphrase"`)) {
		t.Fatalf("completed selection = %q, %v", selection, err)
	}
	if _, exists, err := readPassphraseMigrationRecord(paths); err != nil || exists {
		t.Fatalf("migration journal after success = exists:%t err:%v", exists, err)
	}
	resolved, err := resolveEverydaySecureStorageForPlatform(paths, serviceOptions{}, platform.IsWSL2())
	if err != nil || resolved.vaultMode == platform.VaultModePassphrase {
		t.Fatalf("repeat startup selection = %#v, %v", resolved, err)
	}
	if _, err := resolveEverydaySecureStorageForPlatform(paths, serviceOptions{vaultMode: platform.VaultModePassphrase}, platform.IsWSL2()); apperrors.Code(err) != apperrors.VaultMigrationRequired {
		t.Fatalf("old passphrase vault accepted after completed migration: %v", err)
	}
	fixture.Close()
	var rejected bytes.Buffer
	if code := runServiceStartWithDependencies(paths, serviceOptions{vaultMode: platform.VaultModePassphrase}, io.Discard, &rejected, nil, openServiceStoreWithVaultMode, nil); code != exitFailure || !strings.Contains(rejected.String(), apperrors.VaultMigrationRequired) {
		t.Fatalf("service activated old vault after migration: code:%d stderr:%q", code, rejected.String())
	}
}

func TestPassphraseMigrationRacingOwnerFailsWithoutSuccess(t *testing.T) {
	paths := launchTestPaths(t)
	seedPassphraseMigrationProfile(t, paths, "correct")
	if err := rememberEverydaySecureStorage(paths, platform.VaultModePassphrase); err != nil {
		t.Fatal(err)
	}
	var owner *platform.Owner
	t.Cleanup(func() {
		if owner != nil {
			_ = owner.Close()
		}
	})
	starter := func(startPaths platform.Paths, options serviceOptions) error {
		var err error
		owner, err = platform.Acquire(startPaths, platform.OwnerOptions{})
		if err != nil {
			return err
		}
		var ignored bytes.Buffer
		if code := runServiceStartWithDependencies(startPaths, options, io.Discard, &ignored, nil, openServiceStoreWithVaultMode, nil); code != exitFailure {
			return errors.New("migration request unexpectedly reused an existing owner")
		}
		return nil
	}
	var stdout, stderr bytes.Buffer
	code := ensureEverydayCompanion(paths, serviceOptions{}, strings.NewReader("1\n"), &stdout, &stderr, starter)
	if code != exitFailure || strings.Contains(stdout.String(), "migration complete") || !strings.Contains(stderr.String(), apperrors.VaultMigrationRequired) {
		t.Fatalf("racing owner migration = code:%d stdout:%q stderr:%q", code, stdout.String(), stderr.String())
	}
}

func TestPassphraseMigrationFailuresKeepOriginalStateRecoverable(t *testing.T) {
	for _, test := range []struct {
		name       string
		passphrase string
		factory    nativeVaultFactory
	}{
		{name: "wrong passphrase", passphrase: "wrong", factory: func(platform.Paths, platform.VaultMode, bool) (vault.Vault, error) {
			return vault.NewInMemoryVault(bytes.Repeat([]byte{0x41}, 32), "native-generation")
		}},
		{name: "destination unavailable", passphrase: "correct", factory: func(platform.Paths, platform.VaultMode, bool) (vault.Vault, error) {
			return nil, errors.New("injected destination failure")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := launchTestPaths(t)
			seedPassphraseMigrationProfile(t, paths, "correct")
			if err := rememberEverydaySecureStorage(paths, platform.VaultModePassphrase); err != nil {
				t.Fatal(err)
			}
			target := platform.VaultModeSecretService
			if platform.IsWSL2() {
				target = platform.VaultModeWSLDPAPI
			}
			sourceStore, err := openServiceStoreWithVaultMode(paths, platform.VaultModePassphrase, test.passphrase)
			if err == nil {
				_, err = migrateOpenedPassphraseStoreWithVaultFactory(paths, target, sourceStore, test.factory)
			}
			if err == nil {
				t.Fatal("failed migration reported success")
			}
			if _, exists, err := readPassphraseMigrationRecord(paths); err != nil || exists {
				t.Fatalf("failed migration journal = exists:%t err:%v", exists, err)
			}
			stateStore, err := openServiceStoreWithVaultMode(paths, platform.VaultModePassphrase, "correct")
			if err != nil {
				t.Fatalf("original passphrase state did not reopen: %v", err)
			}
			defer stateStore.Close()
			profileState, err := stateStore.GetProfile(context.Background(), "Work")
			if err != nil || profileState.Status != profile.StatusReady {
				t.Fatalf("retained profile = %#v, %v", profileState, err)
			}
		})
	}
}

func TestPassphraseMigrationDeclineContinuesExplicitPassphraseOperation(t *testing.T) {
	paths := launchTestPaths(t)
	const passphrase = "continue private passphrase"
	seedPassphraseMigrationProfile(t, paths, passphrase)
	if err := rememberEverydaySecureStorage(paths, platform.VaultModePassphrase); err != nil {
		t.Fatal(err)
	}
	var fixture *productionCompanionFixture
	starter := func(startPaths platform.Paths, options serviceOptions) error {
		if options.vaultMode != platform.VaultModePassphrase || options.migrationTarget != "" {
			return errors.New("declined migration did not preserve passphrase mode")
		}
		fixture = startProductionCompanionFixture(startPaths, options, openServiceStoreWithVaultMode)
		return nil
	}
	t.Cleanup(func() { fixture.Close() })
	var stdout, stderr bytes.Buffer
	code := ensureEverydayCompanion(paths, serviceOptions{}, strings.NewReader("2\n"+passphrase+"\n"), &stdout, &stderr, starter)
	if code != exitSuccess || !strings.Contains(stdout.String(), "dashboard address:") || strings.Contains(stdout.String(), "migration complete") {
		t.Fatalf("continued passphrase result = code:%d stdout:%q stderr:%q", code, stdout.String(), stderr.String())
	}
	selection, err := os.ReadFile(paths.SecureStorageFile)
	if err != nil || !bytes.Contains(selection, []byte(`"mode":"passphrase"`)) {
		t.Fatalf("continued passphrase selection = %q, %v", selection, err)
	}
}

func TestInterruptedPassphraseMigrationRestoresOrCompletesFromJournal(t *testing.T) {
	for _, databaseReady := range []bool{false, true} {
		name := "restores original"
		if databaseReady {
			name = "completes verified destination"
		}
		t.Run(name, func(t *testing.T) {
			paths := launchTestPaths(t)
			seedPassphraseMigrationProfile(t, paths, "correct")
			sourceStore, err := openServiceStoreWithVaultMode(paths, platform.VaultModePassphrase, "correct")
			if err != nil {
				t.Fatal(err)
			}
			backup, err := sourceStore.CreateVaultMigrationBackup(context.Background())
			if err != nil {
				_ = sourceStore.Close()
				t.Fatal(err)
			}
			destinationKey := bytes.Repeat([]byte{0x53}, 32)
			factory := func(platform.Paths, platform.VaultMode, bool) (vault.Vault, error) {
				return vault.NewInMemoryVault(destinationKey, "resume-native-generation")
			}
			if databaseReady {
				destination, err := factory(paths, platform.VaultModeSecretService, true)
				if err != nil {
					t.Fatal(err)
				}
				if err := sourceStore.ReprotectVaultState(context.Background(), destination); err != nil {
					t.Fatal(err)
				}
			}
			if err := sourceStore.Close(); err != nil {
				t.Fatal(err)
			}
			target := platform.VaultModeSecretService
			if platform.IsWSL2() {
				target = platform.VaultModeWSLDPAPI
			}
			if err := writePassphraseMigrationRecord(paths, passphraseMigrationRecord{Target: target, BackupID: backup.ID, DatabaseReady: databaseReady}); err != nil {
				t.Fatal(err)
			}
			completed, err := resumeInterruptedPassphraseMigrationWithVaultFactory(paths, target, factory)
			if err != nil || completed != databaseReady {
				t.Fatalf("resume result = completed:%t err:%v", completed, err)
			}
			if _, exists, err := readPassphraseMigrationRecord(paths); err != nil || exists {
				t.Fatalf("journal after resume = exists:%t err:%v", exists, err)
			}
			if databaseReady {
				destination, _ := factory(paths, target, false)
				reopened, err := store.OpenWithVault(paths.DatabaseFile, destination)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				if err := reopened.VerifyProtectedState(context.Background()); err != nil {
					t.Fatal(err)
				}
				return
			}
			reopened, err := openServiceStoreWithVaultMode(paths, platform.VaultModePassphrase, "correct")
			if err != nil {
				t.Fatalf("restored passphrase database did not reopen: %v", err)
			}
			defer reopened.Close()
			if _, err := reopened.GetProfile(context.Background(), "Work"); err != nil {
				t.Fatalf("restored profile unavailable: %v", err)
			}
		})
	}
}

func TestPassphraseServiceReconcilesInterruptedMigrationBeforeUnlock(t *testing.T) {
	paths := launchTestPaths(t)
	seedPassphraseMigrationProfile(t, paths, "correct")
	if err := rememberEverydaySecureStorage(paths, platform.VaultModePassphrase); err != nil {
		t.Fatal(err)
	}
	source, err := openServiceStoreWithVaultMode(paths, platform.VaultModePassphrase, "correct")
	if err != nil {
		t.Fatal(err)
	}
	backup, err := source.CreateVaultMigrationBackup(context.Background())
	if err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	destination, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x79}, 32), "interrupted-native-generation")
	if err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	if err := source.ReprotectVaultState(context.Background(), destination); err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	target := platform.VaultModeSecretService
	if platform.IsWSL2() {
		target = platform.VaultModeWSLDPAPI
	}
	if err := writePassphraseMigrationRecord(paths, passphraseMigrationRecord{Target: target, BackupID: backup.ID}); err != nil {
		t.Fatal(err)
	}
	var resumed atomic.Bool
	resuming := make(chan struct{})
	fixture := startMigrationProductionCompanionFixture(paths, serviceOptions{vaultMode: platform.VaultModePassphrase}, func(resumePaths platform.Paths, resumeTarget platform.VaultMode) (bool, error) {
		resumed.Store(true)
		close(resuming)
		return resumeInterruptedPassphraseMigrationWithVaultFactory(resumePaths, resumeTarget, func(platform.Paths, platform.VaultMode, bool) (vault.Vault, error) {
			return destination, nil
		})
	}, nil)
	defer fixture.Close()
	select {
	case <-resuming:
	case <-time.After(companionStartupTimeout):
		t.Fatalf("passphrase service did not reach migration recovery; stderr:%q", fixture.stderr.String())
	}
	connection, err := waitForCompanionClient(paths, companionStartupTimeout, "", false)
	if err != nil {
		t.Fatalf("passphrase service did not start after recovery: %v; stderr:%q", err, fixture.stderr.String())
	}
	client := httpapi.NewCommandClient(connection.Origin, connection.Token, nil)
	health, err := client.UnlockVault(context.Background(), "correct")
	if err != nil || health.ServiceState != httpapi.ServiceStateReady || !resumed.Load() {
		t.Fatalf("recovered service unlock = health:%#v resumed:%t err:%v", health, resumed.Load(), err)
	}
	if _, exists, err := readPassphraseMigrationRecord(paths); err != nil || exists {
		t.Fatalf("journal after passphrase service recovery = exists:%t err:%v", exists, err)
	}
	selection, err := os.ReadFile(paths.SecureStorageFile)
	if err != nil || !bytes.Contains(selection, []byte(`"mode":"passphrase"`)) {
		t.Fatalf("recovered selection = %q, %v", selection, err)
	}
}

func TestInterruptedMigrationRestoresPassphraseSelectionWhenDestinationFails(t *testing.T) {
	paths := launchTestPaths(t)
	seedPassphraseMigrationProfile(t, paths, "correct")
	source, err := openServiceStoreWithVaultMode(paths, platform.VaultModePassphrase, "correct")
	if err != nil {
		t.Fatal(err)
	}
	backup, err := source.CreateVaultMigrationBackup(context.Background())
	if err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	destination, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x4a}, 32), "failed-reopen-generation")
	if err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	if err := source.ReprotectVaultState(context.Background(), destination); err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rememberEverydaySecureStorage(paths, platform.VaultModeSecretService); err != nil {
		t.Fatal(err)
	}
	if err := writePassphraseMigrationRecord(paths, passphraseMigrationRecord{Target: platform.VaultModeSecretService, BackupID: backup.ID, DatabaseReady: true}); err != nil {
		t.Fatal(err)
	}
	resuming := make(chan struct{})
	fixture := startMigrationProductionCompanionFixture(paths, serviceOptions{vaultMode: platform.VaultModeSecretService}, func(resumePaths platform.Paths, target platform.VaultMode) (bool, error) {
		close(resuming)
		return resumeInterruptedPassphraseMigrationWithVaultFactory(resumePaths, target, func(platform.Paths, platform.VaultMode, bool) (vault.Vault, error) {
			return nil, errors.New("destination unavailable")
		})
	}, nil)
	defer fixture.Close()
	select {
	case <-resuming:
	case <-time.After(companionStartupTimeout):
		t.Fatalf("native service did not reach migration recovery; stderr:%q", fixture.stderr.String())
	}
	connection, err := waitForCompanionClient(paths, companionStartupTimeout, "", false)
	if err != nil {
		t.Fatalf("recovered passphrase service did not start: %v; stderr:%q", err, fixture.stderr.String())
	}
	client := httpapi.NewCommandClient(connection.Origin, connection.Token, nil)
	health, err := client.UnlockVault(context.Background(), "correct")
	if err != nil || health.ServiceState != httpapi.ServiceStateReady {
		t.Fatalf("restored passphrase state did not unlock: health:%#v err:%v; stderr:%q", health, err, fixture.stderr.String())
	}
	selection, err := os.ReadFile(paths.SecureStorageFile)
	if err != nil || !bytes.Contains(selection, []byte(`"mode":"passphrase"`)) {
		t.Fatalf("restored selection = %q, %v", selection, err)
	}
	fixture.Close()
	reopened, err := openServiceStoreWithVaultMode(paths, platform.VaultModePassphrase, "correct")
	if err != nil {
		t.Fatalf("restored passphrase source did not reopen: %v", err)
	}
	defer reopened.Close()
	if err := reopened.VerifyProtectedState(context.Background()); err != nil {
		t.Fatalf("restored protected state did not authenticate: %v", err)
	}
}

func startMigrationProductionCompanionFixture(paths platform.Paths, options serviceOptions, resume serviceVaultMigrationResumer, migrate serviceVaultMigrationRunner) *productionCompanionFixture {
	fixture := &productionCompanionFixture{stop: make(chan struct{}), done: make(chan int, 1)}
	waitForStop := func(owner *platform.Owner, stateOwner interface{ Close() error }, server *httpapi.Server, _ serviceOptions, _, stderr io.Writer, _ bool, _ <-chan error, _ diagnostics.Sink) int {
		<-fixture.stop
		return closeServiceAfterStop(server, stateOwner, owner, stderr, nil)
	}
	go func() {
		fixture.done <- runServiceStartWithDependenciesAndMigration(paths, options, io.Discard, &fixture.stderr, nil, openServiceStoreWithVaultMode, waitForStop, resume, migrate)
	}()
	return fixture
}

func seedPassphraseMigrationProfile(t *testing.T, paths platform.Paths, passphrase string) (string, string) {
	t.Helper()
	home := filepath.Join(paths.Root, "managed-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	credentialPath := filepath.Join(home, "auth.json")
	if err := os.WriteFile(credentialPath, []byte(`{"fixture":"codex-owned-credential"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stateStore, err := openServiceStoreWithVaultMode(paths, platform.VaultModePassphrase, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: "profile-1", Alias: "Work", DisplayName: "Work"}); err != nil {
		_ = stateStore.Close()
		t.Fatal(err)
	}
	if err := stateStore.SetManagedHome(ctx, "profile-1", "profile-1", home); err != nil {
		_ = stateStore.Close()
		t.Fatal(err)
	}
	for _, stage := range []profile.SetupStage{profile.StageDiscovery, profile.StageHome, profile.StageAuthentication, profile.StageValidation} {
		if err := stateStore.SaveSetupStage(ctx, "profile-1", stage); err != nil {
			_ = stateStore.Close()
			t.Fatal(err)
		}
	}
	if _, err := stateStore.PromotePendingProfile(ctx, "profile-1"); err != nil {
		_ = stateStore.Close()
		t.Fatal(err)
	}
	if _, err := stateStore.CompleteInitialSelection(ctx, "profile-1", "", ""); err != nil {
		_ = stateStore.Close()
		t.Fatal(err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}
	return home, credentialPath
}
