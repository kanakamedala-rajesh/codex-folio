package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/buildinfo"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/vault"
)

func TestSelectCLIReportsPersistedSelectionAndRunningLaunchWarning(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	seedSecondReadyProfile(t, paths, secureVault)
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("open store error = %v", err)
	}
	plan, err := stateStore.PrepareLaunch(context.Background(), launchPrepareRequest(paths, "Work"))
	if err != nil {
		t.Fatalf("PrepareLaunch() error = %v", err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), plan.LeaseID, 4321); err != nil {
		t.Fatalf("MarkManagedLaunchStarted() error = %v", err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runSelectWithDependencies(
		[]string{"personal", "--json"}, strings.NewReader(""), &stdout, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
			return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
		}, nil,
	)
	if code != exitSuccess || stderr.Len() != 0 {
		t.Fatalf("exit/stderr = %d/%q, want success", code, stderr.String())
	}
	var result profile.SelectionResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("selection JSON error = %v; output = %q", err, stdout.String())
	}
	if result.Profile.Alias != "Personal" || !result.Profile.Selected || len(result.Warnings) != 1 {
		t.Fatalf("result = %#v, want selected Personal and warning", result)
	}
}

func TestSelectCLIReusesRunningServiceWithoutOpeningStore(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	seedSecondReadyProfile(t, paths, secureVault)
	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("open store error = %v", err)
	}
	selector, err := profile.NewSelector(stateStore)
	if err != nil {
		t.Fatalf("NewSelector() error = %v", err)
	}
	const token = "private-running-service-token"
	server, err := httpapi.NewServer(httpapi.Options{Selection: selector, CommandToken: token})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: token}); err != nil {
		t.Fatalf("PublishClient() error = %v", err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = stateStore.Close()
		_ = owner.Close()
	})
	if _, err := httpapi.NewCommandClient(server.Origin(), "wrong-token", nil).SetSelection(context.Background(), "personal"); apperrors.Code(err) != apperrors.HTTPAPISessionInvalid {
		t.Fatalf("unauthorized command error = %v, want %s", err, apperrors.HTTPAPISessionInvalid)
	}

	var stdout, stderr bytes.Buffer
	code := runSelectWithDependencies(
		[]string{"personal"}, strings.NewReader(""), &stdout, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		func(platform.Paths, platform.VaultMode, string) (*store.Store, error) {
			return nil, errors.New("running-service selection must not open the store")
		}, nil,
	)
	if code != exitSuccess || stderr.Len() != 0 || !strings.Contains(stdout.String(), "Selected Profile: Personal") {
		t.Fatalf("exit/stdout/stderr = %d/%q/%q, want running-service success", code, stdout.String(), stderr.String())
	}
	selection, err := httpapi.NewCommandClient(server.Origin(), token, nil).GetSelection(context.Background())
	if err != nil || len(selection.Profiles) == 0 || selection.Profiles[0].Alias != "Personal" || !selection.Profiles[0].Selected {
		t.Fatalf("service selection/error = %#v/%v, want selected Personal first", selection, err)
	}

	stdout.Reset()
	if code := runServiceStatus(paths, serviceOptions{json: true}, &stdout, &stderr); code != exitSuccess || strings.Contains(stdout.String(), token) {
		t.Fatalf("status exit/output = %d/%q, want redacted success", code, stdout.String())
	}
}

func TestInteractiveSelectionCancelsWithoutLaunchingOrChangingSelection(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	seedSecondReadyProfile(t, paths, secureVault)
	launched := false
	var stdout, stderr bytes.Buffer
	code := runInteractiveSelectionWithDependencies(
		strings.NewReader("q\n"), &stdout, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
			return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
		},
		func(string) int { launched = true; return exitSuccess }, nil,
	)
	if code != exitSuccess || launched || stderr.Len() != 0 || !strings.Contains(stdout.String(), "* 1. Work") {
		t.Fatalf("exit/launched/stdout/stderr = %d/%t/%q/%q", code, launched, stdout.String(), stderr.String())
	}
	assertSelectedAlias(t, paths, secureVault, "Work")
}

func TestInteractiveSelectionUpdatesDefaultThenUsesForegroundLauncher(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	seedSecondReadyProfile(t, paths, secureVault)
	var launchedAlias string
	serviceRunningAtLaunch := false
	code := runInteractiveSelectionWithDependencies(
		strings.NewReader("2\n"), io.Discard, io.Discard,
		func(*string) (platform.Paths, error) { return paths, nil },
		func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
			return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
		},
		func(alias string) int {
			launchedAlias = alias
			status, err := platform.Discover(paths, platform.OwnerOptions{})
			serviceRunningAtLaunch = err == nil && status.Running
			return 23
		}, nil,
	)
	if code != 23 || launchedAlias != "Personal" || !serviceRunningAtLaunch {
		t.Fatalf("exit/alias/service-running = %d/%q/%t, want foreground status 23, Personal, and one live owner", code, launchedAlias, serviceRunningAtLaunch)
	}
	assertSelectedAlias(t, paths, secureVault, "Personal")
}

func TestInteractiveSelectionEnterReusesPersistentOwnerAndPreservesChildInput(t *testing.T) {
	if os.Getenv("CODEX_FOLIO_SELECTION_CHILD") == "1" {
		os.Exit(runSelectionFakeCodexChild())
	}

	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	fixture, err := startEverydayCompanionFixture(paths, secureVault)
	if err != nil {
		t.Fatalf("start full companion fixture: %v", err)
	}
	t.Cleanup(fixture.Close)
	executable := writeSelectionFakeCodex(t, paths.Root)
	childLog := filepath.Join(paths.Root, "child-input.txt")
	t.Setenv("CODEX_FOLIO_SELECTION_CHILD", "1")
	t.Setenv("CODEX_FOLIO_SELECTION_CHILD_LOG", childLog)
	input, inputWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	t.Cleanup(func() {
		_ = input.Close()
		_ = inputWriter.Close()
	})
	if _, err := io.WriteString(inputWriter, "\nchild input\n"); err != nil {
		t.Fatalf("write terminal input error = %v", err)
	}

	openCalls := 0
	var stdout, stderr bytes.Buffer
	result := make(chan int, 1)
	go func() {
		result <- runWithServicePathResolverAndCodexResolverAndForegroundDependencies(
			[]string{"--state-root", paths.Root, "--vault-mode", string(platform.VaultModePassphrase)},
			input, &stdout, &stderr, buildinfo.Metadata{},
			func(*string) (platform.Paths, error) { return paths, nil },
			launchTestResolver{candidate: launch.Candidate{Path: executable, Version: "0.1.2"}},
			func(paths platform.Paths, mode platform.VaultMode, passphrase string) (*store.Store, error) {
				openCalls++
				return nil, fmt.Errorf("persistent owner must remain the sole writer (mode=%q, passphrase-present=%t)", mode, passphrase != "")
			},
			newForegroundProcess, nil,
		)
	}()

	var code int
	select {
	case code = <-result:
	case <-time.After(3 * time.Second):
		_ = inputWriter.Close()
		code = <-result
		t.Fatalf("plain picker launch did not return while native terminal input remained open; eventual code = %d", code)
	}
	if content, err := os.ReadFile(childLog); err != nil || string(content) != "child input\n" {
		t.Fatalf("fake Codex child input = %q, %v; want preserved input", content, err)
	}
	if code != 29 || openCalls != 0 || !strings.Contains(stderr.String(), "passphrase storage requires an unlock on every companion restart") {
		t.Fatalf("result = code:%d opens:%d stdout:%q stderr:%q", code, openCalls, stdout.String(), stderr.String())
	}
}

func runSelectionFakeCodexChild() int {
	if strings.Contains(strings.Join(os.Args, " "), "app-server --stdio") {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			switch {
			case strings.Contains(scanner.Text(), `"method":"initialize"`):
				_, _ = fmt.Fprintln(os.Stdout, `{"id":1,"result":{}}`)
			case strings.Contains(scanner.Text(), `"method":"account/read"`):
				_, _ = fmt.Fprintln(os.Stdout, `{"id":2,"result":{"account":{"type":"chatgpt"}}}`)
			case strings.Contains(scanner.Text(), `"method":"account/rateLimits/read"`):
				_, _ = fmt.Fprintln(os.Stdout, `{"id":3,"result":{"rateLimits":{}}}`)
			}
		}
		return 0
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	_ = os.WriteFile(os.Getenv("CODEX_FOLIO_SELECTION_CHILD_LOG"), []byte(line), 0o600)
	return 29
}

func writeSelectionFakeCodex(t *testing.T, directory string) string {
	t.Helper()
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	if runtime.GOOS == "windows" {
		path := filepath.Join(directory, "fake-codex.cmd")
		content := fmt.Sprintf("@echo off\r\n\"%s\" -test.run=TestInteractiveSelectionEnterReusesPersistentOwnerAndPreservesChildInput -- %%*\r\nexit /b %%errorlevel%%\r\n", testExecutable)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(fake Codex) error = %v", err)
		}
		return path
	}
	path := filepath.Join(directory, "fake-codex")
	content := fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestInteractiveSelectionEnterReusesPersistentOwnerAndPreservesChildInput$ -- \"$@\"\n", testExecutable)
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("WriteFile(fake Codex) error = %v", err)
	}
	return path
}

func TestInteractiveSelectionReusesRunningServiceThroughForegroundLaunch(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("open service store error = %v", err)
	}
	selector, err := profile.NewSelector(stateStore)
	if err != nil {
		t.Fatalf("NewSelector() error = %v", err)
	}
	launches, err := newLaunchCommandService(stateStore, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("newLaunchCommandService() error = %v", err)
	}
	const token = "interactive-running-service-token"
	server, err := httpapi.NewServer(httpapi.Options{Selection: selector, Launches: launches, CommandToken: token})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: token}); err != nil {
		t.Fatalf("PublishClient() error = %v", err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = stateStore.Close()
		_ = owner.Close()
	})

	input := bufio.NewReader(strings.NewReader("\nrunning-service child input"))
	process := &launchTestProcess{pid: 9123, exitStatus: 31}
	openedStore := false
	childInput := ""
	var stderr bytes.Buffer
	code := runInteractiveSelectionWithDependencies(
		input, io.Discard, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		func(platform.Paths, platform.VaultMode, string) (*store.Store, error) {
			openedStore = true
			return nil, errors.New("running service must own state")
		},
		func(alias string) int {
			return runLaunchWithInputAndDependenciesAndOwnerOptions(
				[]string{alias, "--"}, input, io.Discard, &stderr,
				func(*string) (platform.Paths, error) { return paths, nil },
				launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "fake-codex"), Version: "0.1.2"}},
				func(platform.Paths, platform.VaultMode, string) (*store.Store, error) {
					openedStore = true
					return nil, errors.New("running service must own state")
				},
				func(_ launch.Plan, remaining io.Reader, _, _ io.Writer) (foregroundProcess, error) {
					content, readErr := io.ReadAll(remaining)
					if readErr != nil {
						t.Fatalf("ReadAll(child input) error = %v", readErr)
					}
					childInput = string(content)
					return process, nil
				}, nil, platform.OwnerOptions{},
			)
		}, nil,
	)
	if code != 31 || openedStore || !process.started || childInput != "running-service child input" || stderr.Len() != 0 {
		t.Fatalf("result = code:%d opened-store:%t started:%t child-input:%q stderr:%q", code, openedStore, process.started, childInput, stderr.String())
	}
}

func TestInteractiveSelectionEnterDoesNotSubstituteWhenNoProfileIsSelected(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault, err := vault.NewInMemoryVault(make([]byte, 32), "selection-no-default")
	if err != nil {
		t.Fatalf("NewInMemoryVault() error = %v", err)
	}
	seedAdditionalReadyProfile(t, paths, secureVault, "profile-1", "Work", "work-home")
	launched := false
	var stderr bytes.Buffer
	code := runInteractiveSelectionWithDependencies(
		strings.NewReader("\n"), io.Discard, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
			return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
		},
		func(string) int { launched = true; return exitSuccess }, nil,
	)
	if code != exitFailure || launched || !strings.Contains(stderr.String(), apperrors.ProfileNotSelectable) {
		t.Fatalf("result = code:%d launched:%t stderr:%q", code, launched, stderr.String())
	}
}

func seedSecondReadyProfile(t *testing.T, paths platform.Paths, secureVault vault.Vault) {
	seedAdditionalReadyProfile(t, paths, secureVault, "profile-2", "Personal", "personal-home")
}

func seedAdditionalReadyProfile(t *testing.T, paths platform.Paths, secureVault vault.Vault, id, alias, homeName string) {
	t.Helper()
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("open store error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	ctx := context.Background()
	home := filepath.Join(paths.Root, homeName)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("MkdirAll(home) error = %v", err)
	}
	if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: id, Alias: alias, DisplayName: alias}); err != nil {
		t.Fatalf("CreatePendingProfile() error = %v", err)
	}
	if err := stateStore.SetManagedHome(ctx, id, id, home); err != nil {
		t.Fatalf("SetManagedHome() error = %v", err)
	}
	for _, stage := range []profile.SetupStage{profile.StageDiscovery, profile.StageHome, profile.StageAuthentication, profile.StageValidation} {
		if err := stateStore.SaveSetupStage(ctx, id, stage); err != nil {
			t.Fatalf("SaveSetupStage(%s) error = %v", stage, err)
		}
	}
	if _, err := stateStore.PromotePendingProfile(ctx, id); err != nil {
		t.Fatalf("PromotePendingProfile() error = %v", err)
	}
}

func assertSelectedAlias(t *testing.T, paths platform.Paths, secureVault vault.Vault, want string) {
	t.Helper()
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("open store error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	profiles, err := stateStore.ListEligibleProfiles(context.Background())
	if err != nil {
		t.Fatalf("ListEligibleProfiles() error = %v", err)
	}
	if len(profiles) == 0 || profiles[0].Alias != want || !profiles[0].Selected {
		t.Fatalf("profiles = %#v, want selected %q first", profiles, want)
	}
}

func launchPrepareRequest(paths platform.Paths, alias string) launch.PrepareRequest {
	return launch.PrepareRequest{Alias: alias, Executable: filepath.Join(paths.Root, "codex"), WorkingDirectory: paths.Root}
}

func TestInteractiveSelectionPreservesChildExitWhenOwnerCleanupFails(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	var stderr bytes.Buffer
	code := runInteractiveSelectionWithDependencies(strings.NewReader("\n"), io.Discard, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
			return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
		}, func(string) int {
			// A nonempty directory at the metadata path makes owner cleanup fail.
			if err := os.Remove(paths.MetadataFile); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(paths.MetadataFile, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(paths.MetadataFile, "obstacle"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			return 23
		}, nil)
	if code != 23 || stderr.Len() == 0 {
		t.Fatalf("exit=%d stderr=%q; want child status and cleanup diagnostic", code, stderr.String())
	}
}
