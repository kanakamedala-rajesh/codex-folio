package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/vault"
)

type launchTestResolver struct{ candidate launch.Candidate }

func (resolver launchTestResolver) Resolve(string) (launch.Candidate, error) {
	return resolver.candidate, nil
}

type launchTestProcess struct {
	pid        int
	exitStatus int
	waitErr    error
	waitFn     func() error
	started    bool
	killed     bool
	signals    []os.Signal
}

func (process *launchTestProcess) Start() error {
	process.started = true
	return nil
}

func (process *launchTestProcess) Wait() error {
	if process.waitFn != nil {
		return process.waitFn()
	}
	return process.waitErr
}

func (process *launchTestProcess) PID() int { return process.pid }

func (process *launchTestProcess) Signal(signal os.Signal) error {
	process.signals = append(process.signals, signal)
	return nil
}

func (process *launchTestProcess) Kill() error {
	process.killed = true
	return nil
}

func (process *launchTestProcess) ExitStatus() int { return process.exitStatus }

func TestLaunchCLIForwardsPlanStreamsAndChildStatus(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	var gotPlan launch.Plan
	process := &launchTestProcess{pid: 7777, exitStatus: 17}
	var stdout, stderr bytes.Buffer
	resultCode := runLaunchWithInputAndDependenciesAndOwnerOptions(
		[]string{"work", "--", "--model", "value with spaces"},
		strings.NewReader("terminal input"), &stdout, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "codex"), Version: "0.1.2"}},
		func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
			return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
		},
		func(plan launch.Plan, _ io.Reader, output, _ io.Writer) (foregroundProcess, error) {
			gotPlan = plan
			_, _ = io.WriteString(output, "codex output\n")
			return process, nil
		},
		nil,
		platform.OwnerOptions{},
	)
	if resultCode != 17 {
		t.Fatalf("exit code = %d, want child status 17; stdout = %q; stderr = %q", resultCode, stdout.String(), stderr.String())
	}
	if !process.started || process.killed || stdout.String() != "codex output\n" || stderr.Len() != 0 {
		t.Fatalf("process/output = started:%t killed:%t stdout:%q stderr:%q", process.started, process.killed, stdout.String(), stderr.String())
	}
	if gotPlan.LeaseID == "" || !strings.HasSuffix(gotPlan.Executable, "codex") || len(gotPlan.Arguments) != 2 || gotPlan.Arguments[1] != "value with spaces" {
		t.Fatalf("plan = %#v, want exact launch inputs", gotPlan)
	}
	if gotPlan.Environment["CODEX_HOME"] == "" {
		t.Fatalf("plan environment = %#v, want CODEX_HOME delta", gotPlan.Environment)
	}

	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("reopen store error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	record, err := stateStore.GetManagedLaunch(context.Background(), gotPlan.LeaseID)
	if err != nil {
		t.Fatalf("GetManagedLaunch() error = %v", err)
	}
	if record.State != launch.StateExited || record.ProcessID != 7777 || record.ExitStatus == nil || *record.ExitStatus != 17 {
		t.Fatalf("record = %#v, want exited child lifecycle", record)
	}
}

func TestLaunchCLIReturnsChildStatusWhenExitReportFails(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	var stateStore *store.Store
	process := &launchTestProcess{pid: 7777, exitStatus: 0}
	process.waitFn = func() error { return stateStore.Close() }
	var stderr bytes.Buffer
	resultCode := runLaunchWithInputAndDependenciesAndOwnerOptions(
		[]string{"work", "--"}, strings.NewReader(""), io.Discard, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "codex"), Version: "0.1.2"}},
		func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
			var err error
			stateStore, err = store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
			return stateStore, err
		},
		func(launch.Plan, io.Reader, io.Writer, io.Writer) (foregroundProcess, error) { return process, nil },
		nil,
		platform.OwnerOptions{},
	)
	if resultCode != 0 {
		t.Fatalf("exit code = %d, want child status 0; stderr = %q", resultCode, stderr.String())
	}
}

func TestLaunchCLIRejectsPendingProfileWithoutStartingCodex(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault, err := vault.NewInMemoryVault(make([]byte, 32), "launch-pending")
	if err != nil {
		t.Fatalf("NewInMemoryVault() error = %v", err)
	}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	if err := stateStore.CreatePendingProfile(context.Background(), profile.PendingProfile{ID: "pending-1", Alias: "work", DisplayName: "Work"}); err != nil {
		_ = stateStore.Close()
		t.Fatalf("CreatePendingProfile() error = %v", err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	started := false
	var stdout, stderr bytes.Buffer
	resultCode := runLaunchWithInputAndDependenciesAndOwnerOptions(
		[]string{"work", "--"}, strings.NewReader(""), &stdout, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "codex"), Version: "0.1.2"}},
		func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
			return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
		},
		func(launch.Plan, io.Reader, io.Writer, io.Writer) (foregroundProcess, error) {
			started = true
			return nil, errors.New("must not start")
		},
		nil,
		platform.OwnerOptions{},
	)
	if resultCode != exitFailure || started || stdout.Len() != 0 || !strings.Contains(stderr.String(), apperrors.LaunchProfileUnavailable) {
		t.Fatalf("exit/started/stdout/stderr = %d/%t/%q/%q, want unavailable without process", resultCode, started, stdout.String(), stderr.String())
	}
}

func TestNativeForegroundProcessForwardsStreamsAndStatus(t *testing.T) {
	if os.Getenv("CODEX_FOLIO_FOREGROUND_HELPER") == "1" {
		input, _ := io.ReadAll(os.Stdin)
		_, _ = os.Stdout.Write(input)
		_, _ = io.WriteString(os.Stderr, os.Getenv("CODEX_HOME"))
		os.Exit(23)
	}

	executable, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("Abs(test binary) error = %v", err)
	}
	workingDirectory := t.TempDir()
	plan := launch.Plan{
		Executable:       executable,
		WorkingDirectory: workingDirectory,
		Arguments:        []string{"-test.run=^TestNativeForegroundProcessForwardsStreamsAndStatus$"},
		Environment:      map[string]string{"CODEX_FOLIO_FOREGROUND_HELPER": "1", "CODEX_HOME": workingDirectory},
	}
	var stdout, stderr bytes.Buffer
	process, err := newForegroundProcess(plan, strings.NewReader("native input"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("newForegroundProcess() error = %v", err)
	}
	if err := process.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := process.Wait(); err == nil {
		t.Fatal("Wait() error = nil, want non-zero child status")
	}
	if process.ExitStatus() != 23 || stdout.String() != "native input" || stderr.String() != workingDirectory {
		t.Fatalf("status/stdout/stderr = %d/%q/%q, want 23/native input/%q", process.ExitStatus(), stdout.String(), stderr.String(), workingDirectory)
	}
}

func launchTestPaths(t *testing.T) platform.Paths {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll(state) error = %v", err)
	}
	return platform.Paths{
		Root:         root,
		Runtime:      filepath.Join(root, "runtime"),
		LockFile:     filepath.Join(root, "runtime", "service.owner.lock"),
		MetadataFile: filepath.Join(root, "runtime", "service.owner.json"),
		DatabaseFile: filepath.Join(root, "codex-folio.sqlite3"),
		VaultFile:    filepath.Join(root, "codex-folio.vault"),
		ManagedHomes: filepath.Join(root, "managed-homes"),
	}
}

func seedReadyLaunchProfile(t *testing.T, paths platform.Paths) vault.Vault {
	t.Helper()
	secureVault, err := vault.NewInMemoryVault(make([]byte, 32), "launch-cli")
	if err != nil {
		t.Fatalf("NewInMemoryVault() error = %v", err)
	}
	home := filepath.Join(paths.Root, "managed-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("MkdirAll(home) error = %v", err)
	}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	ctx := context.Background()
	if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: "profile-1", Alias: "Work", DisplayName: "Work"}); err != nil {
		_ = stateStore.Close()
		t.Fatalf("CreatePendingProfile() error = %v", err)
	}
	if err := stateStore.SetManagedHome(ctx, "profile-1", "profile-1", home); err != nil {
		_ = stateStore.Close()
		t.Fatalf("SetManagedHome() error = %v", err)
	}
	for _, stage := range []profile.SetupStage{profile.StageDiscovery, profile.StageHome, profile.StageAuthentication, profile.StageValidation} {
		if err := stateStore.SaveSetupStage(ctx, "profile-1", stage); err != nil {
			_ = stateStore.Close()
			t.Fatalf("SaveSetupStage(%s) error = %v", stage, err)
		}
	}
	if _, err := stateStore.PromotePendingProfile(ctx, "profile-1"); err != nil {
		_ = stateStore.Close()
		t.Fatalf("PromotePendingProfile() error = %v", err)
	}
	if _, err := stateStore.CompleteInitialSelection(ctx, "profile-1"); err != nil {
		_ = stateStore.Close()
		t.Fatalf("CompleteInitialSelection() error = %v", err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return secureVault
}
