package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/vault"
)

type vaultCommandLifecycle struct {
	health     httpapi.ServiceHealth
	passphrase string
}

func (lifecycle *vaultCommandLifecycle) Health() httpapi.ServiceHealth { return lifecycle.health }

func (lifecycle *vaultCommandLifecycle) Unlock(_ context.Context, passphrase string) error {
	lifecycle.passphrase = passphrase
	lifecycle.health = httpapi.ServiceHealth{ServiceState: httpapi.ServiceStateReady, VaultState: httpapi.VaultStateUnlocked, DatabaseState: httpapi.DatabaseStateReady}
	return nil
}

func TestLockedServiceLifecycleSuppressesStateUntilOneSuccessfulUnlock(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "state.sqlite3")
	paths := platform.Paths{DatabaseFile: databasePath}
	var opens atomic.Int32
	opener := func(_ platform.Paths, _ platform.VaultMode, passphrase string) (*store.Store, error) {
		opens.Add(1)
		if passphrase != "correct passphrase" {
			return nil, apperrors.New(apperrors.VaultKeyInvalid, errors.New("private invalid-passphrase cause"))
		}
		secureVault, err := vault.NewMemoryVault(vault.MemoryVaultOptions{})
		if err != nil {
			return nil, err
		}
		return store.OpenWithVault(databasePath, secureVault)
	}
	var activations atomic.Int32
	lifecycle := newLockedServiceLifecycle(paths, platform.VaultModePassphrase, opener, func(*store.Store) (httpapi.OperationalServices, error) {
		return httpapi.OperationalServices{}, nil
	})
	lifecycle.SetActivator(func(httpapi.OperationalServices) error {
		activations.Add(1)
		return nil
	})

	if health := lifecycle.Health(); health.ServiceState != httpapi.ServiceStateLocked || health.DatabaseState != httpapi.DatabaseStateNotChecked {
		t.Fatalf("initial health = %#v", health)
	}
	if err := lifecycle.Unlock(context.Background(), "wrong passphrase"); apperrors.Code(err) != apperrors.VaultKeyInvalid {
		t.Fatalf("failed unlock = %v, want %s", err, apperrors.VaultKeyInvalid)
	}
	if activations.Load() != 0 {
		t.Fatal("failed unlock activated state-owning workflows")
	}

	const callers = 8
	var group sync.WaitGroup
	group.Add(callers)
	for range callers {
		go func() {
			defer group.Done()
			if err := lifecycle.Unlock(context.Background(), "correct passphrase"); err != nil {
				t.Errorf("Unlock() error = %v", err)
			}
		}()
	}
	group.Wait()
	if opens.Load() != 2 {
		t.Fatalf("store opens = %d, want one failed and one successful attempt", opens.Load())
	}
	if activations.Load() != 1 {
		t.Fatalf("workflow activations = %d, want 1", activations.Load())
	}
	if health := lifecycle.Health(); health.ServiceState != httpapi.ServiceStateReady || health.VaultState != httpapi.VaultStateUnlocked || health.DatabaseState != httpapi.DatabaseStateReady {
		t.Fatalf("ready health = %#v", health)
	}
	if err := lifecycle.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	restarted := newLockedServiceLifecycle(paths, platform.VaultModePassphrase, opener, func(*store.Store) (httpapi.OperationalServices, error) {
		return httpapi.OperationalServices{}, nil
	})
	if health := restarted.Health(); health.ServiceState != httpapi.ServiceStateLocked || health.VaultState != httpapi.VaultStateLocked {
		t.Fatalf("restarted health = %#v, want locked", health)
	}
}

func TestLockedServiceLifecycleActivatesRealServerThroughAuthenticatedUnlock(t *testing.T) {
	root := testServiceTempDir(t)
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("Chmod(state root) error = %v", err)
	}
	override := root
	paths, err := platform.ResolvePaths(platform.PathOptions{
		Platform:          platform.Platform(runtime.GOOS),
		HomeDir:           filepath.Join(root, "home"),
		OwnerHomeDir:      filepath.Join(root, "owner-home"),
		StateRootOverride: &override,
		Environment:       map[string]string{},
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}

	const passphrase = "real lifecycle passphrase"
	seedVault, err := platform.NewPassphraseVaultWithOptions(paths.VaultFile, platform.PassphraseOptions{AllowCreate: true})
	if err != nil {
		t.Fatalf("NewPassphraseVaultWithOptions() error = %v", err)
	}
	if err := seedVault.Unlock(context.Background(), passphrase); err != nil {
		t.Fatalf("seed vault Unlock() error = %v", err)
	}
	seedStore, err := store.OpenWithVault(paths.DatabaseFile, seedVault)
	if err != nil {
		t.Fatalf("seed store OpenWithVault() error = %v", err)
	}
	if err := seedStore.Close(); err != nil {
		t.Fatalf("seed Store.Close() error = %v", err)
	}
	seedVault.Lock()

	opener := func(paths platform.Paths, _ platform.VaultMode, supplied string) (*store.Store, error) {
		secureVault, err := platform.NewPassphraseVaultWithOptions(paths.VaultFile, platform.PassphraseOptions{AllowCreate: false})
		if err != nil {
			return nil, err
		}
		if err := secureVault.Unlock(context.Background(), supplied); err != nil {
			return nil, err
		}
		stateStore, err := store.OpenWithVault(paths.DatabaseFile, secureVault)
		if err != nil {
			secureVault.Lock()
			return nil, err
		}
		return stateStore, nil
	}
	lifecycle := newLockedServiceLifecycle(paths, platform.VaultModePassphrase, opener, func(stateStore *store.Store) (httpapi.OperationalServices, error) {
		return composeServiceOperationalServices(paths, stateStore)
	})
	defer lifecycle.Close()
	server, err := httpapi.NewServer(httpapi.Options{
		CommandToken:     "real-lifecycle-command-token",
		ServiceLifecycle: lifecycle,
		StartLocked:      true,
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer server.Close()
	lifecycle.SetActivator(server.Activate)
	listener, err := server.Listen()
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()

	command := httpapi.NewCommandClient(server.Origin(), "real-lifecycle-command-token", nil)
	profilesStatus := func() int {
		t.Helper()
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.Origin()+httpapi.CommandProfilesPath, nil)
		if err != nil {
			t.Fatalf("NewRequestWithContext() error = %v", err)
		}
		request.Header.Set("Origin", server.Origin())
		request.Header.Set(httpapi.CommandTokenHeader, "real-lifecycle-command-token")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("profiles request error = %v", err)
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	if status := profilesStatus(); status != http.StatusLocked {
		t.Fatalf("profiles status before unlock = %d, want %d", status, http.StatusLocked)
	}
	if _, err := command.UnlockVault(context.Background(), "wrong passphrase"); apperrors.Code(err) != apperrors.VaultKeyInvalid {
		t.Fatalf("UnlockVault() wrong passphrase error = %v, want %s", err, apperrors.VaultKeyInvalid)
	}
	if status := profilesStatus(); status != http.StatusLocked {
		t.Fatalf("profiles status after failed unlock = %d, want %d", status, http.StatusLocked)
	}
	if _, err := command.UnlockVault(context.Background(), passphrase); err != nil {
		t.Fatalf("UnlockVault() error = %v", err)
	}
	profiles, err := command.ListProfiles(context.Background())
	if err != nil {
		t.Fatalf("ListProfiles() after successful unlock error = %v", err)
	}
	if len(profiles.Profiles) != 0 {
		t.Fatalf("ListProfiles() after successful unlock = %#v, want empty real store", profiles.Profiles)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("Server.Close() error = %v", err)
	}
	if err := <-serveErrors; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
}

func TestLockedServiceLifecycleSurfacesRecoveryWithoutResetting(t *testing.T) {
	lifecycle := newLockedServiceLifecycle(platform.Paths{}, platform.VaultModePassphrase, func(platform.Paths, platform.VaultMode, string) (*store.Store, error) {
		return nil, apperrors.New(apperrors.StoreIntegrityFailed, errors.New("private database detail"))
	}, func(*store.Store) (httpapi.OperationalServices, error) {
		return httpapi.OperationalServices{}, nil
	})
	lifecycle.SetActivator(func(httpapi.OperationalServices) error { return nil })
	if err := lifecycle.Unlock(context.Background(), "passphrase"); apperrors.Code(err) != apperrors.StoreIntegrityFailed {
		t.Fatalf("Unlock() error = %v, want %s", err, apperrors.StoreIntegrityFailed)
	}
	health := lifecycle.Health()
	if health.ServiceState != httpapi.ServiceStateRecoveryRequired || health.DatabaseState != httpapi.DatabaseStateRecoveryRequired || health.ErrorCode != apperrors.StoreIntegrityFailed {
		t.Fatalf("recovery health = %#v", health)
	}
	if len(health.GuidanceCommands) != 2 {
		t.Fatalf("recovery guidance = %#v", health.GuidanceCommands)
	}
}

func TestVaultUnlockCommandUsesPrivateInputAndExistingOwner(t *testing.T) {
	home := testServiceTempDir(t)
	stateRoot := filepath.Join(testServiceTempDir(t), "state")
	override := stateRoot
	paths, err := platform.ResolvePaths(platform.PathOptions{
		Platform: platform.Platform(runtime.GOOS), HomeDir: home, OwnerHomeDir: home,
		StateRootOverride: &override, Environment: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	lifecycle := &vaultCommandLifecycle{health: httpapi.ServiceHealth{
		ServiceState: httpapi.ServiceStateLocked, VaultState: httpapi.VaultStateLocked,
		DatabaseState: httpapi.DatabaseStateNotChecked, GuidanceCommands: []string{"codex-folio vault unlock"},
	}}
	server, err := httpapi.NewServer(httpapi.Options{CommandToken: "vault-command-token", ServiceLifecycle: lifecycle, StartLocked: true})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: "vault-command-token"}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()

	const passphrase = "private command passphrase"
	var stdout, stderr bytes.Buffer
	exitCode := runVault([]string{"unlock", "--state-root", stateRoot, "--json"}, bytes.NewBufferString(passphrase+"\n"), &stdout, &stderr, testServicePathResolver(home))
	if exitCode != exitSuccess {
		t.Fatalf("runVault() exit = %d; stderr = %q", exitCode, stderr.String())
	}
	if lifecycle.passphrase != passphrase {
		t.Fatal("running owner did not receive the supplied passphrase")
	}
	if bytes.Contains(stdout.Bytes(), []byte(passphrase)) || bytes.Contains(stderr.Bytes(), []byte(passphrase)) {
		t.Fatalf("vault command output exposed passphrase; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	var health httpapi.ServiceHealth
	if err := json.Unmarshal(stdout.Bytes(), &health); err != nil {
		t.Fatalf("unlock JSON = %q: %v", stdout.String(), err)
	}
	if health.ServiceState != httpapi.ServiceStateReady {
		t.Fatalf("unlock health = %#v", health)
	}
}
