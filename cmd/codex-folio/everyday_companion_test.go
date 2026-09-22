package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/buildinfo"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/vault"
)

type everydayCompanionFixture struct {
	owner      *platform.Owner
	stateOwner *serviceStateOwner
	server     *httpapi.Server
}

type productionCompanionFixture struct {
	stop     chan struct{}
	done     chan int
	stopOnce sync.Once
	stderr   bytes.Buffer
}

func startProductionCompanionFixture(paths platform.Paths, options serviceOptions, openStore profileStoreOpener) *productionCompanionFixture {
	fixture := &productionCompanionFixture{stop: make(chan struct{}), done: make(chan int, 1)}
	waitForStop := func(owner *platform.Owner, stateOwner interface{ Close() error }, server *httpapi.Server, _ serviceOptions, _, stderr io.Writer, _ bool, _ <-chan error, _ diagnostics.Sink) int {
		<-fixture.stop
		return closeServiceAfterStop(server, stateOwner, owner, stderr, nil)
	}
	go func() {
		fixture.done <- runServiceStartWithDependencies(paths, options, io.Discard, &fixture.stderr, nil, openStore, waitForStop)
	}()
	return fixture
}

func (fixture *productionCompanionFixture) Close() {
	if fixture == nil {
		return
	}
	fixture.stopOnce.Do(func() { close(fixture.stop) })
	select {
	case <-fixture.done:
	case <-time.After(3 * time.Second):
	}
}

func runPlainCompanionTest(paths platform.Paths, input string, starter companionProcessStarter) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := runWithServicePathResolverAndCodexResolverAndForegroundDependenciesAndCompanionStarter(
		[]string{"--state-root", paths.Root}, strings.NewReader(input), &stdout, &stderr, buildinfo.Metadata{},
		func(*string) (platform.Paths, error) { return paths, nil },
		launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "unused-codex"), Version: "0.1.2"}},
		func(platform.Paths, platform.VaultMode, string) (*store.Store, error) {
			return nil, errors.New("plain startup must use the production service owner")
		}, nil, nil, starter,
	)
	return code, stdout.String(), stderr.String()
}

func (fixture *everydayCompanionFixture) Close() {
	if fixture == nil {
		return
	}
	if fixture.server != nil {
		_ = fixture.server.Close()
	}
	if fixture.stateOwner != nil {
		_ = fixture.stateOwner.Close()
	}
	if fixture.owner != nil {
		_ = fixture.owner.Close()
	}
}

func startEverydayCompanionFixture(paths platform.Paths, secureVault vault.Vault) (*everydayCompanionFixture, error) {
	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		return nil, err
	}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		_ = owner.Close()
		return nil, err
	}
	services, err := composeServiceOperationalServices(paths, stateStore, false)
	if err != nil {
		_ = stateStore.Close()
		_ = owner.Close()
		return nil, err
	}
	stateOwner := &serviceStateOwner{store: stateStore, background: services.Background, shutdown: services.Shutdown}
	server, err := httpapi.NewServer(serviceServerOptions(nil, "everyday-companion-command", services))
	if err != nil {
		_ = stateOwner.Close()
		_ = owner.Close()
		return nil, err
	}
	listener, err := server.Listen()
	if err != nil {
		_ = server.Close()
		_ = stateOwner.Close()
		_ = owner.Close()
		return nil, err
	}
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: "everyday-companion-command"}); err != nil {
		_ = server.Close()
		_ = stateOwner.Close()
		_ = owner.Close()
		return nil, err
	}
	go func() { _ = server.Serve(listener) }()
	return &everydayCompanionFixture{owner: owner, stateOwner: stateOwner, server: server}, nil
}

func TestPlainStartupStartsFullCompanionAndLeavesItAfterChildExit(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	var fixture *everydayCompanionFixture
	startCalls := 0
	starter := func(startPaths platform.Paths, options serviceOptions) error {
		startCalls++
		if startPaths.Root != paths.Root || options.enrolled {
			return errors.New("plain startup changed the installation context or enrolled the service")
		}
		var err error
		fixture, err = startEverydayCompanionFixture(startPaths, secureVault)
		return err
	}
	t.Cleanup(func() { fixture.Close() })

	process := &launchTestProcess{pid: 8642, exitStatus: 37}
	var stdout, stderr bytes.Buffer
	code := runWithServicePathResolverAndCodexResolverAndForegroundDependenciesAndCompanionStarter(
		[]string{"--state-root", paths.Root}, strings.NewReader("\n"), &stdout, &stderr, buildinfo.Metadata{},
		func(*string) (platform.Paths, error) { return paths, nil },
		launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "fake-codex"), Version: "0.1.2"}},
		func(platform.Paths, platform.VaultMode, string) (*store.Store, error) {
			return nil, errors.New("plain startup must use the persistent service owner")
		},
		func(launch.Plan, io.Reader, io.Writer, io.Writer) (foregroundProcess, error) { return process, nil },
		nil, starter,
	)
	if code != 37 || startCalls != 1 || !process.started {
		t.Fatalf("plain startup result = code:%d starts:%d child-started:%t; stdout=%q stderr=%q", code, startCalls, process.started, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "dashboard address: ") || !strings.Contains(stdout.String(), "reopen with: codex-folio service start") || strings.Contains(stdout.String(), "bootstrap=") {
		t.Fatalf("plain startup dashboard output = %q", stdout.String())
	}
	status, err := platform.Discover(paths, platform.OwnerOptions{})
	if err != nil || !status.Running {
		t.Fatalf("companion after child exit = %#v, %v", status, err)
	}

	stdout.Reset()
	stderr.Reset()
	code = runWithServicePathResolverAndCodexResolverAndForegroundDependenciesAndCompanionStarter(
		[]string{"--state-root", paths.Root}, strings.NewReader("q\n"), &stdout, &stderr, buildinfo.Metadata{},
		func(*string) (platform.Paths, error) { return paths, nil },
		launchTestResolver{}, nil, nil, nil,
		func(platform.Paths, serviceOptions) error { return errors.New("repeat startup must reuse the owner") },
	)
	if code != exitSuccess || startCalls != 1 {
		t.Fatalf("repeat startup result = code:%d starts:%d stdout:%q stderr:%q", code, startCalls, stdout.String(), stderr.String())
	}
}

func TestEverydayCompanionPassphraseUnlockUsesCommandTransportOnce(t *testing.T) {
	paths := launchTestPaths(t)
	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := &vaultCommandLifecycle{health: httpapi.ServiceHealth{
		ServiceState: httpapi.ServiceStateLocked, VaultState: httpapi.VaultStateLocked, DatabaseState: httpapi.DatabaseStateNotChecked,
	}}
	server, err := httpapi.NewServer(httpapi.Options{CommandToken: "everyday-passphrase-command", ServiceLifecycle: lifecycle, StartLocked: true})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: "everyday-passphrase-command"}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); _ = owner.Close() })

	const passphrase = "private one-time passphrase"
	var stdout, stderr bytes.Buffer
	code := ensureEverydayCompanion(paths, serviceOptions{vaultMode: platform.VaultModePassphrase}, strings.NewReader(passphrase+"\nchild input\n"), &stdout, &stderr, func(platform.Paths, serviceOptions) error {
		return errors.New("running locked owner must be reused")
	})
	if code != exitSuccess || lifecycle.passphrase != passphrase {
		t.Fatalf("unlock result = code:%d passphrase-match:%t stdout:%q stderr:%q", code, lifecycle.passphrase == passphrase, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), passphrase) || strings.Contains(stderr.String(), passphrase) {
		t.Fatal("passphrase escaped into command output")
	}
}

func TestPlainStartupOffersRecoverablePassphraseAlternativeAfterSecretServiceFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("passphrase alternative is currently supported on Linux")
	}
	paths := launchTestPaths(t)
	const passphrase = "private fallback passphrase"
	var fixtures []*productionCompanionFixture
	starts := 0
	starter := func(startPaths platform.Paths, options serviceOptions) error {
		starts++
		fixture := startProductionCompanionFixture(startPaths, options, func(openPaths platform.Paths, mode platform.VaultMode, supplied string) (*store.Store, error) {
			if mode != platform.VaultModePassphrase {
				return nil, apperrors.New(apperrors.VaultUnavailable, errors.New("Secret Service fixture is unavailable"))
			}
			return openServiceStoreWithVaultMode(openPaths, mode, supplied)
		})
		fixtures = append(fixtures, fixture)
		return nil
	}
	t.Cleanup(func() {
		for _, fixture := range fixtures {
			fixture.Close()
		}
	})
	code, stdout, stderr := runPlainCompanionTest(paths, "2\n", starter)
	if code != exitFailure || starts != 2 || !strings.Contains(stderr, "CF_VAULT_LOCKED") {
		t.Fatalf("cancelled fallback = code:%d starts:%d stdout:%q stderr:%q service-stderr:%q", code, starts, stdout, stderr, fixtures[len(fixtures)-1].stderr.String())
	}
	selection, err := os.ReadFile(paths.SecureStorageFile)
	if err != nil || !bytes.Contains(selection, []byte(`"mode":"passphrase"`)) {
		t.Fatalf("remembered secure storage = %q, %v", selection, err)
	}
	status, err := platform.Discover(paths, platform.OwnerOptions{})
	if err != nil || !status.Running {
		t.Fatalf("cancelled passphrase owner = %#v, %v", status, err)
	}

	code, stdout, stderr = runPlainCompanionTest(paths, passphrase+"\n", starter)
	if code == exitSuccess || starts != 2 || !strings.Contains(stdout, "dashboard address:") || strings.Contains(stderr, "CF_VAULT_LOCKED") {
		t.Fatalf("resumed fallback = code:%d starts:%d stdout:%q stderr:%q", code, starts, stdout, stderr)
	}
	if _, err := os.Stat(paths.VaultFile); err != nil {
		t.Fatalf("real passphrase provider did not initialize protected material: %v", err)
	}
	if _, err := os.Stat(paths.DatabaseFile); err != nil {
		t.Fatalf("real service composition did not initialize state: %v", err)
	}
	if strings.Contains(stdout, passphrase) || strings.Contains(stderr, passphrase) {
		t.Fatal("passphrase escaped into setup output")
	}
}

func TestPlainStartupSecretServiceCancellationIsRecoverable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Secret Service recovery prompt is Linux-specific")
	}
	paths := launchTestPaths(t)
	starts := 0
	code, stdout, stderr := runPlainCompanionTest(paths, "q\n", func(_ platform.Paths, options serviceOptions) error {
		starts++
		return writeCompanionStartupStatus(options.startupStatus, "CF_VAULT_LOCKED")
	})
	if code != exitFailure || starts != 1 || !strings.Contains(stdout, "[q] cancel") || !strings.Contains(stderr, "CF_VAULT_LOCKED") {
		t.Fatalf("cancel result = code:%d starts:%d stdout:%q stderr:%q", code, starts, stdout, stderr)
	}
	if _, err := os.Stat(paths.SecureStorageFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled setup persisted selection: %v", err)
	}
}

func TestPlainStartupDetectsExistingPassphraseStateBeforeNativeInitialization(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("existing Linux passphrase migration detection is Linux-specific")
	}
	paths := launchTestPaths(t)
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.DatabaseFile, []byte("existing sqlite state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.VaultFile, []byte("CFPVexisting protected material"), 0o600); err != nil {
		t.Fatal(err)
	}
	started := false
	code, _, stderr := runPlainCompanionTest(paths, "", func(platform.Paths, serviceOptions) error {
		started = true
		return nil
	})
	if code != exitFailure || started || !strings.Contains(stderr, "CF_VAULT_MIGRATION_REQUIRED") {
		t.Fatalf("existing passphrase result = code:%d started:%t stderr:%q", code, started, stderr)
	}
}

func TestCompanionStartupHandshakeAcceptsOnlyPrivateRuntimeFile(t *testing.T) {
	paths := launchTestPaths(t)
	statusPath, err := prepareCompanionStartupStatus(paths)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(statusPath) })
	if err := validateCompanionStartupStatusPath(paths, statusPath); err != nil {
		t.Fatalf("valid status path rejected: %v", err)
	}
	writer := &companionStartupDiagnosticWriter{Writer: io.Discard, path: statusPath}
	if _, err := io.WriteString(writer, "codex-folio [CF_VAULT_UNAVAILABLE]: redacted\n"); err != nil {
		t.Fatal(err)
	}
	if got, err := readCompanionStartupStatus(statusPath); err != nil || got != "CF_VAULT_UNAVAILABLE" {
		t.Fatalf("startup status = %q, %v", got, err)
	}
	outside := filepath.Join(paths.Root, "outside.status")
	if err := os.WriteFile(outside, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateCompanionStartupStatusPath(paths, outside); err == nil {
		t.Fatal("status path outside private runtime was accepted")
	}
	metadata := filepath.Join(paths.Runtime, "service.owner.json")
	if err := os.WriteFile(metadata, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateCompanionStartupStatusPath(paths, metadata); err == nil {
		t.Fatal("service metadata was accepted as a startup status file")
	}
}

func TestCompanionPassphraseUsesTerminalWithoutConsumingLaterForegroundInput(t *testing.T) {
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	if _, err := io.WriteString(writer, "private passphrase\n2\nchild input\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	promptInput, childInput := foregroundInputs(input)
	if got := companionPassphraseInputWithTerminalCheck(promptInput, func(int) bool { return false }); got != promptInput {
		t.Fatal("non-terminal passphrase input did not retain the shared buffered reader")
	}
	privateInput := companionPassphraseInputWithTerminalCheck(promptInput, func(fd int) bool {
		return fd == int(input.Fd())
	})
	if privateInput != input {
		t.Fatal("terminal passphrase input did not expose the original terminal file")
	}
	passphrase, err := readOneByteLine(privateInput)
	if err != nil || passphrase != "private passphrase" {
		t.Fatalf("private terminal input = %q, %v", passphrase, err)
	}
	selection, err := readOneByteLine(promptInput)
	if err != nil || selection != "2" {
		t.Fatalf("selection input = %q, %v", selection, err)
	}
	remaining, err := io.ReadAll(childInput)
	if err != nil || string(remaining) != "child input\n" {
		t.Fatalf("child input = %q, %v", remaining, err)
	}
}

func readOneByteLine(input io.Reader) (string, error) {
	var line []byte
	buffer := make([]byte, 1)
	for {
		count, err := input.Read(buffer)
		if count == 1 {
			if buffer[0] == '\n' {
				return string(line), nil
			}
			line = append(line, buffer[0])
		}
		if err != nil {
			return "", err
		}
	}
}

func TestConcurrentEverydayStartsConvergeOnOneOwner(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault, err := vault.NewMemoryVault(vault.MemoryVaultOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var fixture *everydayCompanionFixture
	startCalls := 0
	starter := func(startPaths platform.Paths, _ serviceOptions) error {
		mu.Lock()
		defer mu.Unlock()
		startCalls++
		if fixture != nil {
			return nil
		}
		var startErr error
		fixture, startErr = startEverydayCompanionFixture(startPaths, secureVault)
		return startErr
	}
	t.Cleanup(func() { fixture.Close() })

	ready := make(chan struct{})
	type result struct {
		code   int
		stderr string
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-ready
			var stderr bytes.Buffer
			code := ensureEverydayCompanion(paths, serviceOptions{}, strings.NewReader(""), io.Discard, &stderr, starter)
			results <- result{code: code, stderr: stderr.String()}
		}()
	}
	close(ready)
	for range 2 {
		if result := <-results; result.code != exitSuccess {
			t.Fatalf("concurrent startup exit = %d; stderr = %q", result.code, result.stderr)
		}
	}
	status, err := platform.Discover(paths, platform.OwnerOptions{})
	if err != nil || !status.Running || fixture == nil {
		t.Fatalf("concurrent owner = %#v fixture:%t error:%v", status, fixture != nil, err)
	}
	if startCalls < 1 || startCalls > 2 {
		t.Fatalf("starter calls = %d, want one or two racing attempts converging on one owner", startCalls)
	}
	if health, err := httpapi.NewCommandClient(fixture.server.Origin(), "everyday-companion-command", nil).ServiceHealth(context.Background()); err != nil || health.ServiceState != httpapi.ServiceStateReady {
		t.Fatalf("concurrent owner health = %#v, %v", health, err)
	}
}

func TestDashboardAuthorizationFailureWarnsWithoutBlockingReadyCompanion(t *testing.T) {
	paths := launchTestPaths(t)
	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server, err := httpapi.NewServer(httpapi.Options{CommandToken: "dashboard-degraded-command", Random: bytes.NewReader(make([]byte, 32))})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: "dashboard-degraded-command"}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); _ = owner.Close() })

	var stdout, stderr bytes.Buffer
	code := ensureEverydayCompanion(paths, serviceOptions{}, strings.NewReader(""), &stdout, &stderr, nil)
	if code != exitSuccess || !strings.Contains(stderr.String(), "dashboard authorization is temporarily unavailable") {
		t.Fatalf("degraded dashboard result = code:%d stdout:%q stderr:%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), server.Origin()+"/") || strings.Contains(stdout.String(), "bootstrap=") {
		t.Fatalf("non-secret dashboard output = %q", stdout.String())
	}
}
