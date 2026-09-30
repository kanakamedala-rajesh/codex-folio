package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configbundle"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/usage"
	"venkatasudha.com/codex-folio/internal/vault"
)

type vaultCommandLifecycle struct {
	health     httpapi.ServiceHealth
	passphrase string
}

func TestServiceServerOptionsPreserveConfigurationBundles(t *testing.T) {
	bundles := new(configbundle.Service)
	options := serviceServerOptions(nil, "configuration-bundle-fixture", httpapi.OperationalServices{ConfigurationBundles: bundles})
	if options.ConfigurationBundles != bundles {
		t.Fatal("service server options dropped portable configuration service")
	}
}

func TestPassphraseServiceGuidancePreservesVaultModeAndGeneratedUnlockCommand(t *testing.T) {
	paths := platform.Paths{Root: filepath.Join("root", "state with $name")}
	if got := serviceTerminalCommandSuffix(paths, platform.VaultModePassphrase); len(got) != 2 || got[0] != "--state-root="+paths.Root || got[1] != "--vault-mode=passphrase" {
		t.Fatalf("passphrase terminal suffix = %#v", got)
	}
	if got := serviceTerminalCommandSuffix(paths, platform.VaultModeWSLDPAPI); len(got) != 2 || got[1] != "--vault-mode=wsl-dpapi" {
		t.Fatalf("WSL terminal suffix = %#v", got)
	}
	if got := serviceTerminalCommandSuffix(paths, platform.VaultModeSecretService); len(got) != 1 || got[0] != "--state-root="+paths.Root {
		t.Fatalf("secret-service terminal suffix = %#v", got)
	}

	var stdout, stderr bytes.Buffer
	health := httpapi.ServiceHealth{
		ServiceState:     httpapi.ServiceStateLocked,
		GuidanceCommands: []string{`'/source build/codex-folio' vault unlock '--state-root=/tmp/custom state'`},
	}
	status := platform.OwnerStatus{Running: true}
	if err := writeServiceStateWithEnrollment(&stdout, &stderr, false, status, false, "", health, platform.ServiceEnrollmentStatus{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "run this in terminal: "+health.GuidanceCommands[0]) || strings.Contains(stdout.String(), "run 'codex-folio vault unlock'") {
		t.Fatalf("locked service output = %q", stdout.String())
	}
}

func TestGeneratedVaultUnlockGuidanceIsAcceptedByVaultParser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the PowerShell rendering is covered by the platform-neutral renderer test")
	}
	root := testServiceTempDir(t)
	stateRoot := filepath.Join(root, "state with $name")
	probe := filepath.Join(root, "codex-folio probe")
	if err := os.WriteFile(probe, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	lifecycle := &vaultCommandLifecycle{health: httpapi.ServiceHealth{
		ServiceState: httpapi.ServiceStateLocked, VaultState: httpapi.VaultStateLocked, DatabaseState: httpapi.DatabaseStateNotChecked,
	}}
	server, err := httpapi.NewServer(httpapi.Options{
		CommandToken:          "generated-guidance-command",
		ServiceLifecycle:      lifecycle,
		StartLocked:           true,
		TerminalCommandBase:   []string{probe},
		TerminalCommandSuffix: serviceTerminalCommandSuffix(platform.Paths{Root: stateRoot}, platform.VaultModePassphrase),
		ServiceEnrollment: func() (string, string, bool) {
			return platform.EnrollmentNotInstalled, "systemd-user", true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()

	health, err := httpapi.NewCommandClient(server.Origin(), "generated-guidance-command", nil).ServiceHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(health.GuidanceCommands) != 1 {
		t.Fatalf("unlock guidance = %#v", health.GuidanceCommands)
	}
	invocation := exec.Command("sh", "-c", health.GuidanceCommands[0])
	output, err := invocation.CombinedOutput()
	if err != nil {
		t.Fatalf("generated unlock guidance failed: %v; output = %q", err, output)
	}
	arguments := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(arguments) != 3 || arguments[0] != "vault" || arguments[1] != "unlock" {
		t.Fatalf("generated unlock arguments = %#v", arguments)
	}
	parsed, err := parseVaultCommandOptions(arguments[2:])
	if err != nil {
		t.Fatalf("generated unlock arguments rejected by parser: %v; arguments = %#v", err, arguments)
	}
	if parsed.stateRoot == nil || *parsed.stateRoot != stateRoot {
		t.Fatalf("generated unlock state root = %#v, want %q", parsed.stateRoot, stateRoot)
	}
	if len(health.EnrollmentGuidance) != 2 || !strings.Contains(health.EnrollmentGuidance[1], "--vault-mode=passphrase") {
		t.Fatalf("passphrase enrollment guidance = %#v", health.EnrollmentGuidance)
	}
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

func TestCollectionSchedulerConsentIsIndependentOfEnrollment(t *testing.T) {
	root := testServiceTempDir(t)
	override := root
	paths, err := platform.ResolvePaths(platform.PathOptions{Platform: platform.Platform(runtime.GOOS), HomeDir: filepath.Join(root, "home"), OwnerHomeDir: filepath.Join(root, "home"), StateRootOverride: &override, Environment: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	databasePath := paths.DatabaseFile
	secureVault, err := vault.NewMemoryVault(vault.MemoryVaultOptions{})
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := store.OpenWithVault(databasePath, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()

	onDemand, err := composeServiceOperationalServices(paths, stateStore, false)
	if err != nil {
		t.Fatalf("on-demand composition error = %v", err)
	}
	if onDemand.Background == nil {
		t.Fatal("on-demand service has no consent-aware worker")
	}
	defer onDemand.Background.Close()
	if settings, consent, err := onDemand.CollectionSettings.CollectionSettings(context.Background()); err != nil || consent != usage.CollectionConsentUndecided || settings.ActiveInterval == 0 {
		t.Fatalf("on-demand collection settings = %#v/%v/%v", settings, consent, err)
	}

	enrolled, err := composeServiceOperationalServices(paths, stateStore, true)
	if err != nil {
		t.Fatalf("enrolled composition error = %v", err)
	}
	if enrolled.Background == nil {
		t.Fatal("explicitly enrolled service did not start periodic collection")
	}
	if _, consent, err := enrolled.CollectionSettings.CollectionSettings(context.Background()); err != nil || consent != usage.CollectionConsentUndecided {
		t.Fatalf("enrollment granted collection consent = %s, error = %v", consent, err)
	}
	accepted := usage.CollectionConsentAccepted
	if _, _, err := onDemand.CollectionSettings.SetCollectionSettings(context.Background(), usage.DefaultCollectionSettings(), &accepted); err != nil {
		t.Fatal(err)
	}
	if _, consent, err := onDemand.CollectionSettings.CollectionSettings(context.Background()); err != nil || consent != accepted {
		t.Fatalf("accepted on-demand collection consent = %s, error = %v", consent, err)
	}
	if err := enrolled.Background.Close(); err != nil {
		t.Fatalf("scheduler Close() error = %v", err)
	}
}

func TestEnrolledServiceIdleResourceBudgetOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc resource evidence is Linux-specific")
	}
	if os.Getenv("CODEX_FOLIO_IDLE_RESOURCE_HELPER") != "1" {
		helper := exec.Command(os.Args[0], "-test.run=^TestEnrolledServiceIdleResourceBudgetOnLinux$", "-test.count=1", "-test.v")
		helper.Env = append(os.Environ(), "CODEX_FOLIO_IDLE_RESOURCE_HELPER=1")
		output, err := helper.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated idle resource measurement failed: %v\n%s", err, output)
		}
		t.Logf("isolated idle resource evidence:\n%s", output)
		return
	}

	root := testServiceTempDir(t)
	override := root
	paths, err := platform.ResolvePaths(platform.PathOptions{Platform: platform.PlatformLinux, HomeDir: filepath.Join(root, "home"), OwnerHomeDir: filepath.Join(root, "home"), StateRootOverride: &override, Environment: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	secureVault, err := vault.NewMemoryVault(vault.MemoryVaultOptions{})
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := store.OpenWithVault(paths.DatabaseFile, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	services, err := composeServiceOperationalServices(paths, stateStore, true)
	if err != nil {
		_ = stateStore.Close()
		t.Fatal(err)
	}
	owner := &serviceStateOwner{store: stateStore, background: services.Background}
	defer owner.Close()
	server, err := httpapi.NewServer(serviceServerOptions(nil, "idle-resource-fixture", services))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	time.Sleep(100 * time.Millisecond)
	startTicks, rssBytes, err := linuxProcessResourceSample()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	time.Sleep(2 * time.Second)
	endTicks, rssAfter, err := linuxProcessResourceSample()
	if err != nil {
		t.Fatal(err)
	}
	if rssAfter > rssBytes {
		rssBytes = rssAfter
	}
	cpuPercent := (float64(endTicks-startTicks) / 100 / time.Since(started).Seconds()) * 100
	t.Logf("idle enrolled service: rss=%d bytes cpu=%.3f%% interval=%s; no profiles, unlocked memory vault, composed real SQLite/service/API workflows", rssBytes, cpuPercent, time.Since(started).Round(time.Millisecond))
	if rssBytes >= 75*1024*1024 {
		t.Fatalf("idle resident memory = %d bytes, want < 75 MiB", rssBytes)
	}
	if cpuPercent >= 1 {
		t.Fatalf("idle CPU = %.3f%%, want < 1%%", cpuPercent)
	}
}

func linuxProcessResourceSample() (uint64, uint64, error) {
	stat, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, 0, err
	}
	closing := strings.LastIndexByte(string(stat), ')')
	fields := strings.Fields(string(stat)[closing+1:])
	if closing < 0 || len(fields) < 13 {
		return 0, 0, errors.New("unexpected /proc/self/stat format")
	}
	userTicks, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	systemTicks, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	statm, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, 0, err
	}
	memoryFields := strings.Fields(string(statm))
	if len(memoryFields) < 2 {
		return 0, 0, errors.New("unexpected /proc/self/statm format")
	}
	residentPages, err := strconv.ParseUint(memoryFields[1], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	return userTicks + systemTicks, residentPages * uint64(os.Getpagesize()), nil
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
