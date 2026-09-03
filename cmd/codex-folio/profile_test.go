package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	profilefeature "venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/vault"
)

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
	authenticator := &cliProfileAuthenticator{}
	var stdout, stderr bytes.Buffer
	resultCode := runProfileWithInputAndDependencies(
		[]string{"add", "work", "--device-code", "--non-interactive", "--json"},
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
