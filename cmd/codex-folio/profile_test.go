package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configpack"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	profilefeature "venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/vault"
)

type runningProfileAuthenticationService struct{}

func (runningProfileAuthenticationService) Authenticate(_ context.Context, request httpapi.CommandProfileAuthenticationRequest, output io.Writer) (httpapi.CommandProfileAuthenticationResult, error) {
	_, _ = io.WriteString(output, "device code: ABCD\n")
	result := profilefeature.SetupResult{Profile: profilefeature.IdentityProfile{ID: "profile-1", Alias: request.Alias, Status: profilefeature.StatusReady, Selected: true}}
	return httpapi.CommandProfileAuthenticationResult{Setup: &result}, nil
}

func TestProfileAddCLIReusesRunningService(t *testing.T) {
	paths := launchTestPaths(t)
	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	server, err := httpapi.NewServer(httpapi.Options{ProfileAuthentication: runningProfileAuthenticationService{}, CommandToken: "profile-auth-token"})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: "profile-auth-token"}); err != nil {
		t.Fatalf("PublishClient() error = %v", err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); _ = owner.Close() })
	var stdout, stderr bytes.Buffer
	code := runProfileWithInputAndDependencies(
		[]string{"add", "Work", "--device-code", "--non-interactive", "--json"}, strings.NewReader(""), &stdout, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil }, nil,
		func(platform.Paths, platform.VaultMode, string) (*store.Store, error) {
			return nil, errors.New("must reuse service")
		},
		nil, nil, nil,
	)
	if code != exitSuccess || !strings.Contains(stderr.String(), "device code: ABCD") || !strings.Contains(stdout.String(), `"alias":"Work"`) {
		t.Fatalf("exit/stdout/stderr = %d/%q/%q", code, stdout.String(), stderr.String())
	}
}

func TestProfileAddCLIComposesStoreHomeAndCodexWithoutPrintingHomePath(t *testing.T) {
	stateRoot := filepath.Join(testServiceTempDir(t), "state")
	paths := platform.Paths{
		Root:         stateRoot,
		Runtime:      filepath.Join(stateRoot, "runtime"),
		LockFile:     filepath.Join(stateRoot, "runtime", "service.owner.lock"),
		MetadataFile: filepath.Join(stateRoot, "runtime", "service.owner.json"),
		DatabaseFile: filepath.Join(stateRoot, "codex-folio.sqlite3"),
		VaultFile:    filepath.Join(stateRoot, "codex-folio.vault"),
		ManagedHomes: filepath.Join(stateRoot, "managed-homes"),
	}
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x2d}, 32), "cli-profile-generation")
	if err != nil {
		t.Fatalf("NewInMemoryVault() error = %v", err)
	}
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		t.Fatalf("MkdirAll(state root) error = %v", err)
	}
	authenticator := &cliProfileAuthenticator{}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	pack, err := configpack.NewDraft("shared", "1", map[string]string{"config/base.toml": "model = \"gpt-5\"\n"})
	if err != nil {
		t.Fatalf("NewDraft() error = %v", err)
	}
	if err := stateStore.CreateConfigurationPack(context.Background(), pack); err != nil {
		t.Fatalf("CreateConfigurationPack() error = %v", err)
	}
	if _, err := stateStore.ApproveConfigurationPack(context.Background(), pack.ID, pack.Version); err != nil {
		t.Fatalf("ApproveConfigurationPack() error = %v", err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	resultCode := runProfileWithInputAndDependencies(
		[]string{"add", "work", "--configuration-pack", "shared@1", "--device-code", "--non-interactive", "--json"},
		strings.NewReader(""), &stdout, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		cliProfileResolver{},
		func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
			return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
		},
		func(root string) (profilefeature.ManagedHomeProvisioner, error) {
			return platform.NewManagedHomeProvisioner(root)
		},
		func() profilefeature.Authenticator { return authenticator },
		nil,
	)
	if resultCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr = %q", resultCode, exitSuccess, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var result profilefeature.SetupResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("profile JSON error = %v; output = %q", err, stdout.String())
	}
	if result.Profile.Status != profilefeature.StatusReady || !result.Profile.Selected || result.AuthenticationMethod != profilefeature.AuthMethodDeviceCode {
		t.Fatalf("result = %#v, want ready selected device-code profile", result)
	}
	if strings.Contains(stdout.String(), paths.ManagedHomes) {
		t.Fatalf("profile output exposes managed home path: %q", stdout.String())
	}
	if authenticator.method != profilefeature.AuthMethodDeviceCode || authenticator.identityHome == "" {
		t.Fatalf("authenticator request = %#v, want device-code and target home", authenticator)
	}
	stateStore, err = store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("reopen store error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	assignment, err := stateStore.GetConfigurationPackAssignment(context.Background(), "work")
	if err != nil || assignment.PackID != "shared" || assignment.Version != "1" {
		t.Fatalf("setup assignment = %#v/%v, want approved shared@1", assignment, err)
	}
}

func TestProfileAddCLIRejectsInvalidAliasAsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	resultCode := runProfileWithInputAndDependencies(
		[]string{"add", "../work"}, nil, &stdout, &stderr,
		func(*string) (platform.Paths, error) {
			return platform.Paths{}, errors.New("paths should not be resolved")
		},
		nil, nil, nil, nil, nil,
	)
	if resultCode != exitUsage {
		t.Fatalf("exit code = %d, want %d", resultCode, exitUsage)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), apperrors.ProfileAliasInvalid) || strings.Contains(stderr.String(), "../work") {
		t.Fatalf("stdout/stderr = %q/%q, want stable redacted alias error", stdout.String(), stderr.String())
	}
}

func TestProfileEditAndListCLIExposeOnlySafeMetadata(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	openStore := func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
		return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	}
	var stdout, stderr bytes.Buffer
	code := runProfileWithInputAndDependencies(
		[]string{"edit", "work", "--alias", "client", "--display-name", "Client work", "--email", "user@example.com", "--workspace", "Example", "--json"},
		strings.NewReader(""), &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil }, nil, openStore, nil, nil, nil,
	)
	if code != exitSuccess || stderr.Len() != 0 {
		t.Fatalf("edit exit/stderr = %d/%q, want success", code, stderr.String())
	}
	var edited profilefeature.IdentityProfile
	if err := json.Unmarshal(stdout.Bytes(), &edited); err != nil {
		t.Fatalf("edit JSON error = %v; output = %q", err, stdout.String())
	}
	if edited.ID != "profile-1" || edited.Alias != "client" || edited.DisplayName != "Client work" || edited.Email != "user@example.com" || edited.Workspace != "Example" {
		t.Fatalf("edited profile = %#v", edited)
	}
	if strings.Contains(stdout.String(), paths.ManagedHomes) || strings.Contains(stdout.String(), "identity_home_path") {
		t.Fatalf("edit output exposes Identity Home path: %q", stdout.String())
	}

	stdout.Reset()
	code = runProfileWithInputAndDependencies(
		[]string{"list", "--json"}, strings.NewReader(""), &stdout, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil }, nil, openStore, nil, nil, nil,
	)
	if code != exitSuccess || stderr.Len() != 0 {
		t.Fatalf("list exit/stderr = %d/%q, want success", code, stderr.String())
	}
	var inventory profilefeature.InventoryResult
	if err := json.Unmarshal(stdout.Bytes(), &inventory); err != nil {
		t.Fatalf("inventory JSON error = %v; output = %q", err, stdout.String())
	}
	if len(inventory.Profiles) != 1 || inventory.Profiles[0].Alias != "client" || strings.Contains(stdout.String(), paths.ManagedHomes) {
		t.Fatalf("inventory/output = %#v/%q, want edited safe projection", inventory, stdout.String())
	}
}

func TestProfileAddCLIRegistersReferencedHomeWithoutPrintingItsPath(t *testing.T) {
	stateRoot := filepath.Join(testServiceTempDir(t), "state")
	externalHome := filepath.Join(testServiceTempDir(t), "existing-codex-home")
	if err := os.MkdirAll(externalHome, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	markerPath := filepath.Join(externalHome, "existing-state")
	marker := []byte("Codex-owned state remains external")
	if err := os.WriteFile(markerPath, marker, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	paths := platform.Paths{
		Root:         stateRoot,
		Runtime:      filepath.Join(stateRoot, "runtime"),
		LockFile:     filepath.Join(stateRoot, "runtime", "service.owner.lock"),
		MetadataFile: filepath.Join(stateRoot, "runtime", "service.owner.json"),
		DatabaseFile: filepath.Join(stateRoot, "codex-folio.sqlite3"),
		VaultFile:    filepath.Join(stateRoot, "codex-folio.vault"),
		ManagedHomes: filepath.Join(stateRoot, "managed-homes"),
	}
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x38}, 32), "cli-referenced-profile-generation")
	if err != nil {
		t.Fatalf("NewInMemoryVault() error = %v", err)
	}
	authenticator := &cliProfileAuthenticator{}
	var stdout, stderr bytes.Buffer
	resultCode := runProfileWithInputAndDependencies(
		[]string{"add", "external", "--identity-home", externalHome, "--non-interactive", "--json"},
		strings.NewReader(""), &stdout, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		cliProfileResolver{},
		func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
			return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
		},
		func(root string) (profilefeature.ManagedHomeProvisioner, error) {
			return platform.NewManagedHomeProvisioner(root)
		},
		func() profilefeature.Authenticator { return authenticator },
		nil,
	)
	if resultCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr = %q", resultCode, exitSuccess, stderr.String())
	}
	var result profilefeature.SetupResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("profile JSON error = %v; output = %q", err, stdout.String())
	}
	if result.Profile.Status != profilefeature.StatusReady || !result.Profile.Selected || result.Profile.IdentityHomeOwnership != profilefeature.HomeOwnershipReferenced {
		t.Fatalf("result = %#v, want ready selected referenced profile", result)
	}
	if result.AuthenticationMethod != profilefeature.AuthMethodReused || authenticator.authenticateCalls != 0 {
		t.Fatalf("authentication = method:%q calls:%d, want reused without login", result.AuthenticationMethod, authenticator.authenticateCalls)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want no warning without documented metadata", result.Warnings)
	}
	if strings.Contains(stdout.String(), externalHome) || stderr.Len() != 0 {
		t.Fatalf("stdout/stderr = %q/%q, want redacted JSON and no diagnostics", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(paths.ManagedHomes); !os.IsNotExist(err) {
		t.Fatalf("managed homes stat error = %v, want no managed home creation", err)
	}
	if got, err := os.ReadFile(markerPath); err != nil || !bytes.Equal(got, marker) {
		t.Fatalf("referenced home state = %q/%v, want unchanged marker", got, err)
	}
}

func TestProfileAddCLILeavesInvalidReferencedHomePending(t *testing.T) {
	stateRoot := filepath.Join(testServiceTempDir(t), "state")
	missingHome := filepath.Join(testServiceTempDir(t), "missing-codex-home")
	paths := platform.Paths{
		Root:         stateRoot,
		Runtime:      filepath.Join(stateRoot, "runtime"),
		LockFile:     filepath.Join(stateRoot, "runtime", "service.owner.lock"),
		MetadataFile: filepath.Join(stateRoot, "runtime", "service.owner.json"),
		DatabaseFile: filepath.Join(stateRoot, "codex-folio.sqlite3"),
		VaultFile:    filepath.Join(stateRoot, "codex-folio.vault"),
		ManagedHomes: filepath.Join(stateRoot, "managed-homes"),
	}
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x39}, 32), "cli-invalid-referenced-profile-generation")
	if err != nil {
		t.Fatalf("NewInMemoryVault() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	resultCode := runProfileWithInputAndDependencies(
		[]string{"add", "external", "--identity-home", missingHome, "--device-code", "--non-interactive", "--json"},
		strings.NewReader(""), &stdout, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		cliProfileResolver{},
		func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
			return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
		},
		func(root string) (profilefeature.ManagedHomeProvisioner, error) {
			return platform.NewManagedHomeProvisioner(root)
		},
		func() profilefeature.Authenticator { return &cliProfileAuthenticator{} },
		nil,
	)
	if resultCode != exitFailure || stdout.Len() != 0 {
		t.Fatalf("exit/stdout = %d/%q, want failure and no JSON", resultCode, stdout.String())
	}
	if !strings.Contains(stderr.String(), apperrors.ProfileHomeInvalid) || strings.Contains(stderr.String(), missingHome) {
		t.Fatalf("stderr = %q, want stable redacted home error", stderr.String())
	}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("reopen store error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	pending, err := stateStore.FindPendingProfile(context.Background(), "EXTERNAL")
	if err != nil {
		t.Fatalf("FindPendingProfile() error = %v", err)
	}
	if pending.Status != profilefeature.StatusPending || pending.Stages.Discovery != true || pending.Stages.Home || pending.IdentityHomeID != "" {
		t.Fatalf("pending = %#v, want discovery-only pending profile", pending)
	}
	if _, err := os.Stat(paths.ManagedHomes); !os.IsNotExist(err) {
		t.Fatalf("managed homes stat error = %v, want no managed home creation", err)
	}
}

func TestProfileAddCLIResumesPendingAuthenticationWithRealStore(t *testing.T) {
	stateRoot := filepath.Join(testServiceTempDir(t), "state")
	paths := platform.Paths{
		Root:         stateRoot,
		Runtime:      filepath.Join(stateRoot, "runtime"),
		LockFile:     filepath.Join(stateRoot, "runtime", "service.owner.lock"),
		MetadataFile: filepath.Join(stateRoot, "runtime", "service.owner.json"),
		DatabaseFile: filepath.Join(stateRoot, "codex-folio.sqlite3"),
		ManagedHomes: filepath.Join(stateRoot, "managed-homes"),
	}
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x19}, 32), "resume-profile-generation")
	if err != nil {
		t.Fatalf("NewInMemoryVault() error = %v", err)
	}
	authenticator := &cliProfileAuthenticator{authenticateErr: profilefeature.ErrAuthCancelled}
	openStore := func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
		return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	}
	run := func(output *bytes.Buffer) int {
		return runProfileWithInputAndDependencies(
			[]string{"add", "work", "--device-code", "--non-interactive", "--json"},
			strings.NewReader(""), output, &bytes.Buffer{},
			func(*string) (platform.Paths, error) { return paths, nil },
			cliProfileResolver{}, openStore,
			func(root string) (profilefeature.ManagedHomeProvisioner, error) {
				return platform.NewManagedHomeProvisioner(root)
			},
			func() profilefeature.Authenticator { return authenticator }, nil,
		)
	}
	var firstOutput bytes.Buffer
	if resultCode := run(&firstOutput); resultCode != exitFailure || firstOutput.Len() != 0 {
		t.Fatalf("first run = code %d output %q, want failed silent result", resultCode, firstOutput.String())
	}
	authenticator.authenticateErr = nil
	var resumedOutput bytes.Buffer
	if resultCode := run(&resumedOutput); resultCode != exitSuccess {
		t.Fatalf("resumed run exit code = %d, want %d", resultCode, exitSuccess)
	}
	var result profilefeature.SetupResult
	if err := json.Unmarshal(resumedOutput.Bytes(), &result); err != nil {
		t.Fatalf("resumed JSON error = %v; output = %q", err, resumedOutput.String())
	}
	if !result.Resumed || result.Profile.Status != profilefeature.StatusReady || !result.Profile.Selected {
		t.Fatalf("resumed result = %#v, want resumed ready selected profile", result)
	}
	if authenticator.authenticateCalls != 2 {
		t.Fatalf("authenticate calls = %d, want one failed and one resumed invocation", authenticator.authenticateCalls)
	}
}

func TestProfileAddCLIResumesEveryCommittedStageWithRealStoreAndFakeSeams(t *testing.T) {
	stateRoot := filepath.Join(testServiceTempDir(t), "state")
	paths := platform.Paths{Root: stateRoot, Runtime: filepath.Join(stateRoot, "runtime"), LockFile: filepath.Join(stateRoot, "runtime", "owner.lock"), MetadataFile: filepath.Join(stateRoot, "runtime", "owner.json"), DatabaseFile: filepath.Join(stateRoot, "profiles.sqlite3"), ManagedHomes: filepath.Join(stateRoot, "managed-homes")}
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x61}, 32), "profile-stage-resume")
	if err != nil {
		t.Fatalf("NewInMemoryVault() error = %v", err)
	}
	filesystem := profileTestFileSystem{}
	clock := profileTestClock{now: time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)}
	home := &profileTestHome{fail: true}
	authenticator := &cliProfileAuthenticator{authenticateErr: profilefeature.ErrAuthCancelled, checkErr: profilefeature.ErrValidationFailed}
	selectionFailure := true
	openStore := func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
		return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Clock: clock, Vault: secureVault, ProfileHooks: store.ProfileHooks{BeforeSelection: func(string) error {
			if selectionFailure {
				selectionFailure = false
				return errors.New("simulated selection interruption")
			}
			return nil
		}}})
	}
	resolver := cliProfileResolver{}
	run := func() (profilefeature.SetupResult, int) {
		var stdout, stderr bytes.Buffer
		code := runProfileWithInputAndDependenciesAndOwnerOptions(
			[]string{"add", "work", "--device-code", "--non-interactive", "--json"}, strings.NewReader(""), &stdout, &stderr,
			func(*string) (platform.Paths, error) { return paths, nil }, resolver, openStore,
			func(root string) (profilefeature.ManagedHomeProvisioner, error) {
				provisioner, err := platform.NewManagedHomeProvisionerWithFileSystem(root, filesystem)
				if err == nil {
					home.provisioner = provisioner
				}
				return home, err
			}, func() profilefeature.Authenticator { return authenticator }, nil,
			platform.OwnerOptions{Clock: clock, ProcessID: func() int { return 4242 }, FileSystem: filesystem},
		)
		if code == exitSuccess {
			var result profilefeature.SetupResult
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("profile JSON error = %v; output = %q", err, stdout.String())
			}
			return result, code
		}
		if stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("failed run output = %q/%q, want silent JSON result and diagnostic", stdout.String(), stderr.String())
		}
		return profilefeature.SetupResult{}, code
	}

	if _, code := run(); code != exitFailure {
		t.Fatalf("home interruption code = %d, want %d", code, exitFailure)
	}
	assertProfileStages(t, paths, secureVault, clock, profilefeature.SetupStages{Discovery: true})
	home.fail = false
	if _, code := run(); code != exitFailure {
		t.Fatalf("authentication interruption code = %d, want %d", code, exitFailure)
	}
	assertProfileStages(t, paths, secureVault, clock, profilefeature.SetupStages{Discovery: true, Home: true})
	authenticator.authenticateErr = nil
	if _, code := run(); code != exitFailure {
		t.Fatalf("validation interruption code = %d, want %d", code, exitFailure)
	}
	assertProfileStages(t, paths, secureVault, clock, profilefeature.SetupStages{Discovery: true, Home: true, Authentication: true})
	authenticator.checkErr = nil
	if _, code := run(); code != exitFailure {
		t.Fatalf("selection interruption code = %d, want %d", code, exitFailure)
	}
	assertProfileStages(t, paths, secureVault, clock, profilefeature.SetupStages{Discovery: true, Home: true, Authentication: true, Validation: true})
	result, code := run()
	if code != exitSuccess || !result.Resumed || !result.Stages.Complete() || result.Profile.Status != profilefeature.StatusReady || !result.Profile.Selected {
		t.Fatalf("resumed result = %#v, code = %d, want complete ready selected profile", result, code)
	}
}

func TestProfileReauthenticateCLIUsesExistingHomeAndReturnsSafeResult(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	authenticator := &cliProfileAuthenticator{checkErr: profilefeature.ErrNotAuthenticated}
	var stdout, stderr bytes.Buffer
	resultCode := runProfileWithInputAndDependenciesAndOwnerOptions(
		[]string{"reauthenticate", "work", "--device-code", "--non-interactive", "--json"}, strings.NewReader(""), &stdout, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "codex"), Version: "0.1.2"}},
		func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
			return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
		},
		func(string) (profilefeature.ManagedHomeProvisioner, error) {
			t.Fatal("reauthentication must not provision a new Identity Home")
			return nil, nil
		},
		func() profilefeature.Authenticator { return authenticator }, nil,
		platform.OwnerOptions{},
	)
	if resultCode != exitSuccess || stderr.Len() != 0 {
		t.Fatalf("exit/stderr = %d/%q, want successful quiet JSON result", resultCode, stderr.String())
	}
	var result profilefeature.ReauthenticationResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("reauthentication JSON error = %v; output = %q", err, stdout.String())
	}
	if !result.Reauthenticated || result.Profile.Status != profilefeature.StatusReady || result.AuthenticationMethod != profilefeature.AuthMethodDeviceCode {
		t.Fatalf("result = %#v, want recovered ready device-code profile", result)
	}
	if authenticator.method != profilefeature.AuthMethodDeviceCode || authenticator.authenticateCalls != 1 || strings.Contains(stdout.String(), paths.ManagedHomes) {
		t.Fatalf("auth/output = %#v/%q, want one device-code auth and no home path", authenticator, stdout.String())
	}
}

func TestProfileLifecycleCLIQuarantinesRestoresAndPurgesManagedHome(t *testing.T) {
	paths := launchTestPaths(t)
	paths.ProfileQuarantine = filepath.Join(paths.Root, "profile-quarantine")
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x72}, 32), "profile-lifecycle")
	if err != nil {
		t.Fatalf("NewInMemoryVault() error = %v", err)
	}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	ctx := context.Background()
	for index, item := range []struct{ id, alias string }{{"profile-1", "Work"}, {"profile-2", "Personal"}} {
		home := filepath.Join(paths.ManagedHomes, item.id)
		if err := os.MkdirAll(home, 0o700); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", item.alias, err)
		}
		if err := stateStore.CreatePendingProfile(ctx, profilefeature.PendingProfile{ID: item.id, Alias: item.alias, DisplayName: item.alias}); err != nil {
			t.Fatalf("CreatePendingProfile(%q) error = %v", item.alias, err)
		}
		if err := stateStore.SetManagedHome(ctx, item.id, "home-"+item.id, home); err != nil {
			t.Fatalf("SetManagedHome(%q) error = %v", item.alias, err)
		}
		for _, stage := range []profilefeature.SetupStage{profilefeature.StageDiscovery, profilefeature.StageHome, profilefeature.StageAuthentication, profilefeature.StageValidation} {
			if err := stateStore.SaveSetupStage(ctx, item.id, stage); err != nil {
				t.Fatalf("SaveSetupStage(%q) error = %v", item.alias, err)
			}
		}
		if _, err := stateStore.PromotePendingProfile(ctx, item.id); err != nil {
			t.Fatalf("PromotePendingProfile(%q) error = %v", item.alias, err)
		}
		if index == 0 {
			if _, err := stateStore.CompleteInitialSelection(ctx, item.id, "", ""); err != nil {
				t.Fatalf("CompleteInitialSelection() error = %v", err)
			}
		}
	}
	if err := stateStore.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	marker := filepath.Join(paths.ManagedHomes, "profile-1", "marker")
	if err := os.WriteFile(marker, []byte("owned"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	openStore := func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
		return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	}
	stateStore, err = openStore(paths, "", "")
	if err != nil {
		t.Fatalf("reopen before interruption error = %v", err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatalf("close interruption fixture error = %v", err)
	}
	runLifecycle := func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := runProfileWithInputAndDependencies(args, strings.NewReader(""), &stdout, &stderr,
			func(*string) (platform.Paths, error) { return paths, nil }, nil, openStore, nil, nil, nil)
		return code, stdout.String(), stderr.String()
	}
	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		t.Fatalf("Acquire() running service error = %v", err)
	}
	runningStore, err := openStore(paths, "", "")
	if err != nil {
		t.Fatalf("open running service store error = %v", err)
	}
	lifecycle, err := newProfileLifecycle(paths, runningStore)
	if err != nil {
		t.Fatalf("newProfileLifecycle() error = %v", err)
	}
	const token = "profile-lifecycle-command-token"
	server, err := httpapi.NewServer(httpapi.Options{ProfileLifecycle: lifecycle, CommandToken: token})
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
	plan, err := runningStore.PrepareLaunch(ctx, launch.PrepareRequest{Alias: "Work", Executable: filepath.Join(paths.Root, "codex"), WorkingDirectory: paths.Root})
	if err != nil {
		t.Fatalf("PrepareLaunch() running refusal fixture error = %v", err)
	}
	if err := runningStore.MarkManagedLaunchStarted(ctx, plan.LeaseID, 4242); err != nil {
		t.Fatalf("MarkManagedLaunchStarted() error = %v", err)
	}
	if _, err := httpapi.NewCommandClient(server.Origin(), token, nil).ApplyProfileLifecycle(ctx, "remove", "Work", "Personal", "Work"); apperrors.Code(err) != apperrors.ProfileRemovalBlocked {
		t.Fatalf("running removal error = %v, want %s", err, apperrors.ProfileRemovalBlocked)
	}
	if err := runningStore.MarkManagedLaunchExited(ctx, plan.LeaseID, 0); err != nil {
		t.Fatalf("MarkManagedLaunchExited() error = %v", err)
	}
	if _, err := httpapi.NewCommandClient(server.Origin(), token, nil).ApplyProfileLifecycle(ctx, "remove", "Work", "", "Work"); apperrors.Code(err) != apperrors.ProfileReplacementRequired {
		t.Fatalf("missing replacement error = %v, want %s", err, apperrors.ProfileReplacementRequired)
	}
	{
		work, workErr := runningStore.GetProfile(ctx, "Work")
		personal, personalErr := runningStore.GetProfile(ctx, "Personal")
		if workErr != nil || personalErr != nil || !work.Selected || personal.Selected {
			t.Fatalf("rejected replacement changed profiles: Work=%#v/%v Personal=%#v/%v", work, workErr, personal, personalErr)
		}
	}
	if _, err := runningStore.BeginProfileRemoval(ctx, "Work", "Personal"); err != nil {
		t.Fatalf("BeginProfileRemoval() interruption fixture error = %v", err)
	}
	if _, err := httpapi.NewCommandClient(server.Origin(), token, nil).ApplyProfileLifecycle(ctx, "remove", "work", "Personal", ""); apperrors.Code(err) != apperrors.ProfileConfirmationInvalid {
		t.Fatalf("unconfirmed service apply error = %v, want %s", err, apperrors.ProfileConfirmationInvalid)
	}
	var stdout, stderr bytes.Buffer
	code := runProfileWithInputAndDependencies([]string{"remove", "work", "--replacement", "Personal", "--confirm", "work", "--non-interactive", "--json"}, strings.NewReader(""), &stdout, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil }, nil,
		func(platform.Paths, platform.VaultMode, string) (*store.Store, error) {
			return nil, errors.New("running-service lifecycle must not open the store")
		}, nil, nil, nil)
	diagnostic := stderr.String()
	if code != exitFailure || !strings.Contains(diagnostic, apperrors.ProfileConfirmationInvalid) {
		t.Fatalf("mismatched confirmation = %d/%q", code, diagnostic)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("mismatched confirmation changed managed home: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("close running service error = %v", err)
	}
	if err := runningStore.Close(); err != nil {
		t.Fatalf("close running service store error = %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("close running service owner error = %v", err)
	}

	code, output, diagnostic := runLifecycle("remove", "work", "--replacement", "Personal", "--confirm", "Work", "--non-interactive", "--json")
	if code != exitSuccess || diagnostic != "" || !strings.Contains(output, `"remote_identity_affected":false`) {
		t.Fatalf("remove = %d/%q/%q", code, output, diagnostic)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("managed marker after remove error = %v, want not exist", err)
	}
	code, output, diagnostic = runLifecycle("restore", "Work", "--json")
	if code != exitSuccess || diagnostic != "" || !strings.Contains(output, `"action":"restored"`) {
		t.Fatalf("restore = %d/%q/%q", code, output, diagnostic)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "owned" {
		t.Fatalf("restored marker = %q, error = %v", data, err)
	}
	code, _, diagnostic = runLifecycle("remove", "Work", "--confirm", "Work", "--non-interactive", "--json")
	if code != exitSuccess || diagnostic != "" {
		t.Fatalf("second remove = %d/%q", code, diagnostic)
	}
	code, output, diagnostic = runLifecycle("purge", "Work", "--confirm", "Work", "--non-interactive", "--json")
	if code != exitSuccess || diagnostic != "" || !strings.Contains(output, `"action":"purged"`) {
		t.Fatalf("purge = %d/%q/%q", code, output, diagnostic)
	}
	externalHome := filepath.Join(paths.Root, "external-codex-home")
	externalMarker := filepath.Join(externalHome, "marker")
	if err := os.MkdirAll(externalHome, 0o700); err != nil {
		t.Fatalf("MkdirAll(external) error = %v", err)
	}
	if err := os.WriteFile(externalMarker, []byte("external"), 0o600); err != nil {
		t.Fatalf("WriteFile(external) error = %v", err)
	}
	stateStore, err = openStore(paths, "", "")
	if err != nil {
		t.Fatalf("reopen for referenced profile error = %v", err)
	}
	if err := stateStore.CreatePendingProfile(ctx, profilefeature.PendingProfile{ID: "profile-3", Alias: "External", DisplayName: "External"}); err != nil {
		t.Fatalf("CreatePendingProfile(External) error = %v", err)
	}
	if err := stateStore.SetReferencedHome(ctx, "profile-3", "home-profile-3", externalHome); err != nil {
		t.Fatalf("SetReferencedHome(External) error = %v", err)
	}
	for _, stage := range []profilefeature.SetupStage{profilefeature.StageDiscovery, profilefeature.StageHome, profilefeature.StageAuthentication, profilefeature.StageValidation} {
		if err := stateStore.SaveSetupStage(ctx, "profile-3", stage); err != nil {
			t.Fatalf("SaveSetupStage(External) error = %v", err)
		}
	}
	if _, err := stateStore.PromotePendingProfile(ctx, "profile-3"); err != nil {
		t.Fatalf("PromotePendingProfile(External) error = %v", err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatalf("close referenced fixture error = %v", err)
	}
	code, output, diagnostic = runLifecycle("remove", "External", "--confirm", "External", "--non-interactive", "--json")
	if code != exitSuccess || diagnostic != "" || !strings.Contains(output, `"action":"deregistered"`) {
		t.Fatalf("referenced remove = %d/%q/%q", code, output, diagnostic)
	}
	if data, err := os.ReadFile(externalMarker); err != nil || string(data) != "external" {
		t.Fatalf("external marker = %q, error = %v", data, err)
	}
	stateStore, err = openStore(paths, "", "")
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	if _, err := stateStore.GetQuarantinedProfile(ctx, "Work"); !errors.Is(err, profilefeature.ErrNotFound) {
		t.Fatalf("GetQuarantinedProfile() error = %v, want not found", err)
	}
	if _, err := stateStore.GetProfile(ctx, "External"); !errors.Is(err, profilefeature.ErrNotFound) {
		t.Fatalf("GetProfile(External) error = %v, want not found", err)
	}
	personal, err := stateStore.GetProfile(ctx, "Personal")
	if err != nil || !personal.Selected {
		t.Fatalf("replacement = %#v, error = %v", personal, err)
	}
}

func TestProfileLifecycleCLIRejectsExpiredRestore(t *testing.T) {
	paths := launchTestPaths(t)
	paths.ProfileQuarantine = filepath.Join(paths.Root, "profile-quarantine")
	vaultKey, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x73}, 32), "profile-expiry")
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: vaultKey})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	home := filepath.Join(paths.ManagedHomes, "profile-1")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.CreatePendingProfile(ctx, profilefeature.PendingProfile{ID: "profile-1", Alias: "Work", DisplayName: "Work"}); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.SetManagedHome(ctx, "profile-1", "home-profile-1", home); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []profilefeature.SetupStage{profilefeature.StageDiscovery, profilefeature.StageHome, profilefeature.StageAuthentication, profilefeature.StageValidation} {
		if err := stateStore.SaveSetupStage(ctx, "profile-1", stage); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := stateStore.PromotePendingProfile(ctx, "profile-1"); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", paths.DatabaseFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE profile_quarantine SET purge_after = '2000-01-01T00:00:00Z'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	openStore := func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
		return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: vaultKey})
	}
	var stdout, stderr bytes.Buffer
	code := runProfileWithInputAndDependencies([]string{"remove", "Work", "--confirm", "Work", "--non-interactive", "--json"}, strings.NewReader(""), &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil }, nil, openStore, nil, nil, nil)
	if code != exitSuccess {
		t.Fatalf("remove = %d/%q", code, stderr.String())
	}
	db, err = sql.Open("sqlite", paths.DatabaseFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE profile_quarantine SET purge_after = '2000-01-01T00:00:00Z'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	stdout.Reset()
	stderr.Reset()
	code = runProfileWithInputAndDependencies([]string{"restore", "Work", "--json"}, strings.NewReader(""), &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil }, nil, openStore, nil, nil, nil)
	if code != exitFailure || !strings.Contains(stderr.String(), apperrors.ProfileQuarantineExpired) {
		t.Fatalf("restore = %d/%q, want expiry failure", code, stderr.String())
	}
}

func TestProfileLifecycleServiceCancelsFailedQuarantine(t *testing.T) {
	paths := launchTestPaths(t)
	paths.ProfileQuarantine = filepath.Join(paths.Root, "profile-quarantine")
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x74}, 32), "profile-failure")
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	home := filepath.Join(paths.ManagedHomes, "profile-1")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.CreatePendingProfile(ctx, profilefeature.PendingProfile{ID: "profile-1", Alias: "Work", DisplayName: "Work"}); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.SetManagedHome(ctx, "profile-1", "home-profile-1", home); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []profilefeature.SetupStage{profilefeature.StageDiscovery, profilefeature.StageHome, profilefeature.StageAuthentication, profilefeature.StageValidation} {
		if err := stateStore.SaveSetupStage(ctx, "profile-1", stage); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := stateStore.PromotePendingProfile(ctx, "profile-1"); err != nil {
		t.Fatal(err)
	}
	failingHomes, err := platform.NewProfileHomeLifecycleWithFileSystem(paths.ManagedHomes, paths.ProfileQuarantine, failingProfileHomeFileSystem{renameErr: errors.New("injected quarantine failure")})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := profilefeature.NewLifecycle(stateStore, failingHomes)
	if err != nil {
		t.Fatal(err)
	}
	server, err := httpapi.NewServer(httpapi.Options{ProfileLifecycle: lifecycle, CommandToken: "profile-failure-token"})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	_, err = httpapi.NewCommandClient(server.Origin(), "profile-failure-token", nil).ApplyProfileLifecycle(ctx, "remove", "Work", "", "Work")
	if err == nil {
		t.Fatal("failed quarantine removal succeeded")
	}
	if _, err := stateStore.GetProfile(ctx, "Work"); err != nil {
		t.Fatalf("profile after failed removal = %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	_ = stateStore.Close()
}

type failingProfileHomeFileSystem struct{ renameErr error }

func (failingProfileHomeFileSystem) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }
func (failingProfileHomeFileSystem) MkdirAll(path string, mode os.FileMode) error {
	return os.MkdirAll(path, mode)
}
func (f failingProfileHomeFileSystem) Rename(string, string) error { return f.renameErr }
func (failingProfileHomeFileSystem) RemoveAll(path string) error   { return os.RemoveAll(path) }

func assertProfileStages(t *testing.T, paths platform.Paths, secureVault vault.Vault, clock profileTestClock, want profilefeature.SetupStages) {
	t.Helper()
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Clock: clock, Vault: secureVault})
	if err != nil {
		t.Fatalf("reopen profile store: %v", err)
	}
	pending, err := stateStore.FindPendingProfile(context.Background(), "WORK")
	_ = stateStore.Close()
	if err != nil {
		t.Fatalf("FindPendingProfile() after interruption: %v", err)
	}
	if pending.Stages != want {
		t.Fatalf("durable stages = %#v, want %#v", pending.Stages, want)
	}
}

type profileTestClock struct{ now time.Time }

func (clock profileTestClock) Now() time.Time { return clock.now }

type profileTestHome struct {
	provisioner profilefeature.ManagedHomeProvisioner
	fail        bool
}

func (home *profileTestHome) Ensure(ctx context.Context, profileID string) (string, error) {
	if home.fail {
		return "", errors.New("simulated home interruption")
	}
	return home.provisioner.Ensure(ctx, profileID)
}

type profileTestFileSystem struct{}

func (profileTestFileSystem) Lstat(path string) (os.FileInfo, error) { return os.Lstat(path) }
func (profileTestFileSystem) Stat(path string) (os.FileInfo, error)  { return os.Stat(path) }
func (profileTestFileSystem) MkdirAll(path string, mode os.FileMode) error {
	return os.MkdirAll(path, mode)
}
func (profileTestFileSystem) Chmod(path string, mode os.FileMode) error { return os.Chmod(path, mode) }
func (profileTestFileSystem) OpenFile(path string, flags int, mode os.FileMode) (platform.File, error) {
	return os.OpenFile(path, flags, mode)
}
func (profileTestFileSystem) Open(path string) (platform.File, error) { return os.Open(path) }
func (profileTestFileSystem) Remove(path string) error                { return os.Remove(path) }
func (profileTestFileSystem) Rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}
func (profileTestFileSystem) EnforcePrivatePermissions(string) error   { return nil }
func (profileTestFileSystem) TryExclusiveLock(platform.File) error     { return nil }
func (profileTestFileSystem) ReleaseExclusiveLock(platform.File) error { return nil }
func (profileTestFileSystem) IsLockContention(error) bool              { return false }

type cliProfileResolver struct{}

func (cliProfileResolver) Resolve(string) (launch.Candidate, error) {
	return launch.Candidate{Path: "/opt/codex/bin/codex", Version: "0.1.2"}, nil
}

type cliProfileAuthenticator struct {
	method            profilefeature.AuthMethod
	identityHome      string
	authenticateErr   error
	checkErr          error
	authenticateCalls int
}

func (authenticator *cliProfileAuthenticator) Authenticate(_ context.Context, request profilefeature.AuthenticationRequest) error {
	authenticator.authenticateCalls++
	authenticator.method = request.Method
	authenticator.identityHome = request.IdentityHome
	return authenticator.authenticateErr
}

func (authenticator *cliProfileAuthenticator) Check(context.Context, profilefeature.AuthenticationRequest) error {
	err := authenticator.checkErr
	authenticator.checkErr = nil
	return err
}
