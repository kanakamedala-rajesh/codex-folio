package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"venkatasudha.com/codex-folio/internal/buildinfo"
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
