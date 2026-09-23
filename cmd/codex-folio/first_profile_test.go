package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	"venkatasudha.com/codex-folio/internal/buildinfo"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/vault"
)

type firstProfileAuthenticator struct {
	authenticateCalls int
	checkCalls        int
	failOnce          bool
}

func (auth *firstProfileAuthenticator) Authenticate(context.Context, profile.AuthenticationRequest) error {
	auth.authenticateCalls++
	if auth.failOnce {
		auth.failOnce = false
		return profile.ErrAuthenticationFailed
	}
	return nil
}

func (auth *firstProfileAuthenticator) Check(context.Context, profile.AuthenticationRequest) error {
	auth.checkCalls++
	return nil
}

func startFirstProfileService(t *testing.T, paths platform.Paths, secureVault vault.Vault, auth *firstProfileAuthenticator) *everydayCompanionFixture {
	return startFirstProfileServiceWithCodex(t, paths, secureVault, auth, filepath.Join(paths.Root, "fake-codex"))
}

func startFirstProfileServiceWithCodex(t *testing.T, paths platform.Paths, secureVault vault.Vault, auth profile.Authenticator, executable string) *everydayCompanionFixture {
	t.Helper()
	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		_ = owner.Close()
		t.Fatal(err)
	}
	services, err := composeServiceOperationalServices(paths, stateStore, false)
	if err != nil {
		_ = stateStore.Close()
		_ = owner.Close()
		t.Fatal(err)
	}
	resolver := launchTestResolver{candidate: launch.Candidate{Path: executable, Version: "0.1.2"}}
	services.ProfileAuthentication, err = newProfileAuthenticationCommandService(paths, stateStore, services.ConfigurationPacks, resolver, auth)
	if err != nil {
		_ = stateStore.Close()
		_ = owner.Close()
		t.Fatal(err)
	}
	services.Launches.(*launchCommandService).authenticator = auth
	stateOwner := &serviceStateOwner{store: stateStore, background: services.Background, shutdown: services.Shutdown}
	server, err := httpapi.NewServer(serviceServerOptions(nil, "first-profile-command", services))
	if err != nil {
		_ = stateOwner.Close()
		_ = owner.Close()
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		_ = server.Close()
		_ = stateOwner.Close()
		_ = owner.Close()
		t.Fatal(err)
	}
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: "first-profile-command"}); err != nil {
		_ = server.Close()
		_ = stateOwner.Close()
		_ = owner.Close()
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	return &everydayCompanionFixture{owner: owner, stateOwner: stateOwner, server: server}
}

func runFirstProfileJourney(t *testing.T, paths platform.Paths, input string, auth *firstProfileAuthenticator) (int, string, string, *launchTestProcess) {
	t.Helper()
	process := &launchTestProcess{pid: 4123, exitStatus: 17}
	code, output, diagnostic := runFirstProfileJourneyWithCodex(t, paths, input, auth, filepath.Join(paths.Root, "fake-codex"), func(launch.Plan, io.Reader, io.Writer, io.Writer) (foregroundProcess, error) { return process, nil })
	return code, output, diagnostic, process
}

func runFirstProfileJourneyWithCodex(t *testing.T, paths platform.Paths, input string, auth profile.Authenticator, executable string, newProcess launchProcessFactory) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runWithServicePathResolverAndCodexResolverAndForegroundDependenciesAndCompanionStarter(
		[]string{"--state-root", paths.Root}, strings.NewReader(input), &stdout, &stderr, buildinfo.Metadata{},
		func(*string) (platform.Paths, error) { return paths, nil },
		launchTestResolver{candidate: launch.Candidate{Path: executable, Version: "0.1.2"}},
		func(platform.Paths, platform.VaultMode, string) (*store.Store, error) {
			return nil, errors.New("service must own state")
		},
		newProcess,
		func() profile.Authenticator { return auth },
		func(platform.Paths, serviceOptions) error { return errors.New("running service must be reused") },
	)
	return code, stdout.String(), stderr.String()
}

func TestPlainStartupGuidesManagedSetupThroughServiceAndLaunch(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x71}, 32), "first-profile-managed")
	if err != nil {
		t.Fatal(err)
	}
	auth := &firstProfileAuthenticator{}
	fixture := startFirstProfileService(t, paths, secureVault, auth)
	t.Cleanup(fixture.Close)
	code, output, diagnostic, process := runFirstProfileJourney(t, paths, "m\nwork\nWork account\nb\n1\n", auth)
	if code != 17 || !process.started || auth.authenticateCalls != 1 || !strings.Contains(output, "Profile: work (ready)") || !strings.Contains(output, "Identity Home: managed") || diagnostic != "" {
		t.Fatalf("journey = %d/%q/%q; started=%t auth=%d", code, output, diagnostic, process.started, auth.authenticateCalls)
	}
}

func TestPlainStartupAdoptsAuthenticatedReferencedHomeWithoutLogin(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x72}, 32), "first-profile-referenced")
	if err != nil {
		t.Fatal(err)
	}
	auth := &firstProfileAuthenticator{}
	fixture := startFirstProfileService(t, paths, secureVault, auth)
	t.Cleanup(fixture.Close)
	home := filepath.Join(testServiceTempDir(t), "existing-codex-home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	code, output, diagnostic, process := runFirstProfileJourney(t, paths, "r\nexisting\nExisting account\n"+home+"\n\n1\n", auth)
	if code != 17 || !process.started || auth.authenticateCalls != 0 || auth.checkCalls == 0 || !strings.Contains(output, "Identity Home: referenced") || !strings.Contains(output, "Registration does not consent to history import") || diagnostic != "" {
		t.Fatalf("journey = %d/%q/%q; started=%t auth=%d check=%d", code, output, diagnostic, process.started, auth.authenticateCalls, auth.checkCalls)
	}
}

func TestPlainStartupFailedAuthenticationRemainsPendingAndResumes(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x73}, 32), "first-profile-resume")
	if err != nil {
		t.Fatal(err)
	}
	auth := &firstProfileAuthenticator{failOnce: true}
	fixture := startFirstProfileService(t, paths, secureVault, auth)
	t.Cleanup(fixture.Close)
	code, output, diagnostic, process := runFirstProfileJourney(t, paths, "m\nwork\nWork\nb\nq\n", auth)
	if code != 0 || process.started || !strings.Contains(output, "Resume Work (work)") || !strings.Contains(diagnostic, "setup remains Pending") {
		t.Fatalf("failed journey = %d/%q/%q", code, output, diagnostic)
	}
	client := httpapi.NewCommandClient(fixture.server.Origin(), "first-profile-command", nil)
	inventory, err := client.ListProfiles(context.Background())
	if err != nil || len(inventory.Profiles) != 1 || inventory.Profiles[0].Status != profile.StatusPending {
		t.Fatalf("pending inventory = %#v/%v", inventory, err)
	}
	code, output, diagnostic, process = runFirstProfileJourney(t, paths, "1\n\n1\n", auth)
	if code != 17 || !process.started || !strings.Contains(output, "Profile: work (ready)") || diagnostic != "" {
		t.Fatalf("resumed journey = %d/%q/%q", code, output, diagnostic)
	}
	inventory, err = client.ListProfiles(context.Background())
	if err != nil || len(inventory.Profiles) != 1 || inventory.Profiles[0].Status != profile.StatusReady {
		t.Fatalf("ready inventory = %#v/%v", inventory, err)
	}
}

func TestPlainStartupRunsInstalledCodexAuthenticationAndForeground(t *testing.T) {
	if os.Getenv("CODEX_FOLIO_FIRST_PROFILE_CHILD") == "1" {
		os.Exit(runFirstProfileFakeCodexChild())
	}
	paths := launchTestPaths(t)
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x74}, 32), "first-profile-native")
	if err != nil {
		t.Fatal(err)
	}
	executable := writeFirstProfileFakeCodex(t, paths.Root)
	loginLog := filepath.Join(paths.Root, "login-home.txt")
	launchLog := filepath.Join(paths.Root, "launch-home.txt")
	t.Setenv("CODEX_FOLIO_FIRST_PROFILE_CHILD", "1")
	t.Setenv("CODEX_FOLIO_FIRST_PROFILE_LOGIN_LOG", loginLog)
	t.Setenv("CODEX_FOLIO_FIRST_PROFILE_LAUNCH_LOG", launchLog)
	auth := codexadapter.NewAuthenticator()
	fixture := startFirstProfileServiceWithCodex(t, paths, secureVault, auth, executable)
	t.Cleanup(fixture.Close)
	code, output, diagnostic := runFirstProfileJourneyWithCodex(t, paths, "m\nwork\nWork account\nb\n9\n1\n", auth, executable, newForegroundProcess)
	if code != 17 || !strings.Contains(output, "Profile: work (ready)") || !strings.Contains(output, "Choose an Identity Profile") || !strings.Contains(diagnostic, "choose a listed number") {
		t.Fatalf("native journey = %d/%q/%q", code, output, diagnostic)
	}
	loginHome, err := os.ReadFile(loginLog)
	if err != nil {
		t.Fatal(err)
	}
	launchHome, err := os.ReadFile(launchLog)
	if err != nil {
		t.Fatal(err)
	}
	if string(loginHome) == "" || string(loginHome) != string(launchHome) {
		t.Fatalf("Codex identity home differs between login and foreground: %q/%q", loginHome, launchHome)
	}
}

func writeFirstProfileFakeCodex(t *testing.T, directory string) string {
	t.Helper()
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		path := filepath.Join(directory, "fake-codex.cmd")
		content := fmt.Sprintf("@echo off\r\n\"%s\" -test.run=TestPlainStartupRunsInstalledCodexAuthenticationAndForeground -- %%*\r\nexit /b %%errorlevel%%\r\n", testExecutable)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	path := filepath.Join(directory, "fake-codex")
	content := fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestPlainStartupRunsInstalledCodexAuthenticationAndForeground$ -- \"$@\"\n", testExecutable)
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func runFirstProfileFakeCodexChild() int {
	args := strings.Join(os.Args, " ")
	if strings.Contains(args, "app-server --stdio") {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			switch {
			case strings.Contains(scanner.Text(), `"method":"initialize"`):
				_, _ = fmt.Fprintln(os.Stdout, `{"id":1,"result":{}}`)
			case strings.Contains(scanner.Text(), `"method":"account/read"`):
				account := "null"
				if _, err := os.Stat(os.Getenv("CODEX_FOLIO_FIRST_PROFILE_LOGIN_LOG")); err == nil {
					account = `{"type":"chatgpt"}`
				}
				_, _ = fmt.Fprintf(os.Stdout, `{"id":2,"result":{"account":%s}}`+"\n", account)
			case strings.Contains(scanner.Text(), `"method":"account/rateLimits/read"`):
				_, _ = fmt.Fprintln(os.Stdout, `{"id":3,"result":{}}`)
			}
		}
		return 0
	}
	if strings.Contains(args, " login") {
		if err := os.WriteFile(os.Getenv("CODEX_FOLIO_FIRST_PROFILE_LOGIN_LOG"), []byte(os.Getenv("CODEX_HOME")), 0o600); err != nil {
			return 1
		}
		return 0
	}
	if err := os.WriteFile(os.Getenv("CODEX_FOLIO_FIRST_PROFILE_LAUNCH_LOG"), []byte(os.Getenv("CODEX_HOME")), 0o600); err != nil {
		return 1
	}
	return 17
}
