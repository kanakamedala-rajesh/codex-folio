package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
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
	code := runInteractiveSelectionWithDependencies(
		strings.NewReader("2\n"), io.Discard, io.Discard,
		func(*string) (platform.Paths, error) { return paths, nil },
		func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
			return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
		},
		func(alias string) int { launchedAlias = alias; return 23 }, nil,
	)
	if code != 23 || launchedAlias != "Personal" {
		t.Fatalf("exit/alias = %d/%q, want foreground status 23 and Personal", code, launchedAlias)
	}
	assertSelectedAlias(t, paths, secureVault, "Personal")
}

func seedSecondReadyProfile(t *testing.T, paths platform.Paths, secureVault vault.Vault) {
	t.Helper()
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("open store error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	ctx := context.Background()
	home := filepath.Join(paths.Root, "personal-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("MkdirAll(home) error = %v", err)
	}
	if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: "profile-2", Alias: "Personal", DisplayName: "Personal"}); err != nil {
		t.Fatalf("CreatePendingProfile() error = %v", err)
	}
	if err := stateStore.SetManagedHome(ctx, "profile-2", "profile-2", home); err != nil {
		t.Fatalf("SetManagedHome() error = %v", err)
	}
	for _, stage := range []profile.SetupStage{profile.StageDiscovery, profile.StageHome, profile.StageAuthentication, profile.StageValidation} {
		if err := stateStore.SaveSetupStage(ctx, "profile-2", stage); err != nil {
			t.Fatalf("SaveSetupStage(%s) error = %v", stage, err)
		}
	}
	if _, err := stateStore.PromotePendingProfile(ctx, "profile-2"); err != nil {
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
