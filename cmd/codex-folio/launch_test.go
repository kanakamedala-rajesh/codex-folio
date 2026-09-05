package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configpack"
	"venkatasudha.com/codex-folio/internal/httpapi"
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
	seedSecondReadyProfile(t, paths, secureVault)
	selectionStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("open selection store error = %v", err)
	}
	if _, err := selectionStore.SelectProfile(context.Background(), "Personal"); err != nil {
		t.Fatalf("SelectProfile() error = %v", err)
	}
	if err := selectionStore.Close(); err != nil {
		t.Fatalf("selection store Close() error = %v", err)
	}
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
	profiles, err := stateStore.ListEligibleProfiles(context.Background())
	if err != nil || len(profiles) != 2 || profiles[0].Alias != "Personal" || !profiles[0].Selected {
		t.Fatalf("selection after deterministic launch = %#v/%v, want Personal unchanged", profiles, err)
	}
}

func TestLaunchCLIUsesRunningServiceForLaunchLifecycle(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer func() { _ = owner.Close() }()
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("open service store error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	configurationPacks, err := newConfigurationPackService(stateStore)
	if err != nil {
		t.Fatalf("newConfigurationPackService() error = %v", err)
	}
	launches, err := newLaunchCommandService(stateStore, configurationPacks, &cliProfileAuthenticator{})
	if err != nil {
		t.Fatalf("newLaunchCommandService() error = %v", err)
	}
	const token = "running-launch-token"
	server, err := httpapi.NewServer(httpapi.Options{Launches: launches, CommandToken: token})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer func() { _ = server.Close() }()
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: token}); err != nil {
		t.Fatalf("PublishClient() error = %v", err)
	}
	go func() { _ = server.Serve(listener) }()

	openedStore := false
	process := &launchTestProcess{pid: 7890, exitStatus: 23}
	var gotPlan launch.Plan
	var stderr bytes.Buffer
	code := runLaunchWithInputAndDependenciesAndOwnerOptions(
		[]string{"work", "--", "--model", "gpt-5"}, strings.NewReader("terminal input"), io.Discard, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "codex"), Version: "0.1.2"}},
		func(platform.Paths, platform.VaultMode, string) (*store.Store, error) {
			openedStore = true
			return nil, errors.New("foreground CLI must not open service-owned state")
		},
		func(plan launch.Plan, _ io.Reader, _ io.Writer, _ io.Writer) (foregroundProcess, error) {
			gotPlan = plan
			return process, nil
		}, nil, platform.OwnerOptions{},
	)
	if code != 23 || openedStore || !process.started || stderr.Len() != 0 {
		t.Fatalf("launch = code:%d opened-store:%t started:%t stderr:%q", code, openedStore, process.started, stderr.String())
	}
	record, err := stateStore.GetManagedLaunch(context.Background(), gotPlan.LeaseID)
	if err != nil {
		t.Fatalf("GetManagedLaunch() error = %v", err)
	}
	if record.State != launch.StateExited || record.ProcessID != 7890 || record.ExitStatus == nil || *record.ExitStatus != 23 {
		t.Fatalf("record = %#v, want service-owned exited lifecycle", record)
	}
}

func TestLaunchCLIProjectsAssignedConfigurationPackBeforeStartingCodex(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	home := filepath.Join(paths.Root, "managed-home")
	localPath := filepath.Join(home, "state", "threads.sqlite3")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o700); err != nil {
		t.Fatalf("MkdirAll(local state) error = %v", err)
	}
	if err := os.WriteFile(localPath, []byte("profile-owned"), 0o600); err != nil {
		t.Fatalf("WriteFile(local state) error = %v", err)
	}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("open store error = %v", err)
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
	if _, err := stateStore.AssignConfigurationPack(context.Background(), "Work", pack.ID, pack.Version); err != nil {
		t.Fatalf("AssignConfigurationPack() error = %v", err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatalf("setup store Close() error = %v", err)
	}

	started := false
	process := &launchTestProcess{pid: 7788, exitStatus: 0}
	var stderr bytes.Buffer
	resultCode := runLaunchWithInputAndDependenciesAndOwnerOptions(
		[]string{"work", "--"}, strings.NewReader(""), io.Discard, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "codex"), Version: "0.1.2"}},
		func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
			return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
		},
		func(plan launch.Plan, _ io.Reader, _ io.Writer, _ io.Writer) (foregroundProcess, error) {
			started = true
			content, readErr := os.ReadFile(filepath.Join(home, "config", "base.toml"))
			if readErr != nil || string(content) != "model = \"gpt-5\"\n" {
				t.Fatalf("projected config before process = %q, error = %v", content, readErr)
			}
			return process, nil
		},
		nil,
		platform.OwnerOptions{},
	)
	if resultCode != exitSuccess || !started || stderr.Len() != 0 {
		t.Fatalf("launch result = code:%d started:%t stderr:%q, want projected successful launch", resultCode, started, stderr.String())
	}
	if content, err := os.ReadFile(localPath); err != nil || string(content) != "profile-owned" {
		t.Fatalf("profile-owned state = %q, error = %v, want unchanged", content, err)
	}
}

func TestLaunchCLIDoesNotStartCodexAfterProjectionFailure(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	home := filepath.Join(paths.Root, "managed-home")
	if err := os.MkdirAll(filepath.Join(home, "config", "base.toml"), 0o700); err != nil {
		t.Fatal(err)
	}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatal(err)
	}
	pack, err := configpack.NewDraft("shared", "1", map[string]string{"config/base.toml": "model = \"gpt-5\"\n"})
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.CreateConfigurationPack(context.Background(), pack); err != nil {
		t.Fatal(err)
	}
	if _, err := stateStore.ApproveConfigurationPack(context.Background(), pack.ID, pack.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := stateStore.AssignConfigurationPack(context.Background(), "work", pack.ID, pack.Version); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}

	started := false
	var stderr bytes.Buffer
	code := runLaunchWithInputAndDependenciesAndOwnerOptions(
		[]string{"work", "--"}, strings.NewReader(""), io.Discard, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "codex"), Version: "0.1.2"}},
		func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
			return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
		},
		func(launch.Plan, io.Reader, io.Writer, io.Writer) (foregroundProcess, error) {
			started = true
			return nil, errors.New("must not start")
		}, nil, platform.OwnerOptions{},
	)
	if code != exitFailure || started || !strings.Contains(stderr.String(), apperrors.ConfigurationPackProjectionFailed) || strings.Contains(stderr.String(), home) {
		t.Fatalf("launch = code:%d started:%t stderr:%q, want redacted projection failure before process start", code, started, stderr.String())
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

func TestLaunchCLINeedsReauthenticationBeforeStartingCodex(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	authenticator := &cliProfileAuthenticator{checkErr: profile.ErrNotAuthenticated}
	started := false
	var stdout, stderr bytes.Buffer
	resultCode := runLaunchWithInputAndDependenciesAndOwnerOptionsAndAuthenticator(
		[]string{"work", "--"}, strings.NewReader(""), &stdout, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "codex"), Version: "0.1.2"}},
		func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
			return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
		},
		func(launch.Plan, io.Reader, io.Writer, io.Writer) (foregroundProcess, error) {
			started = true
			return nil, errors.New("Codex must not start before reauthentication")
		},
		nil,
		func() profile.Authenticator { return authenticator },
		platform.OwnerOptions{},
	)
	if resultCode != exitFailure || started || stdout.Len() != 0 || !strings.Contains(stderr.String(), apperrors.ProfileReauthenticationRequired) {
		t.Fatalf("exit/started/stdout/stderr = %d/%t/%q/%q, want reauthentication before process start", resultCode, started, stdout.String(), stderr.String())
	}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("reopen store error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	item, err := stateStore.GetProfile(context.Background(), "work")
	if err != nil {
		t.Fatalf("GetProfile() error = %v", err)
	}
	if item.Status != profile.StatusNeedsReauthentication {
		t.Fatalf("profile status = %q, want needs reauthentication", item.Status)
	}
}

func TestLaunchCLIRecoversAndRepeatedlyLaunchesTwoAuthenticatedProfiles(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	personalHome := seedReferencedReadyProfile(t, paths, secureVault)
	executable := filepath.Join(paths.Root, "codex")
	workHome := filepath.Join(paths.Root, "managed-home")
	authenticated := map[string]bool{personalHome: true}
	checks := map[string]int{}
	loginCalls := 0
	authenticator := codexadapter.NewAuthenticatorWithCommandRunner(func(_ context.Context, gotExecutable string, args, environment []string, _ io.Reader, stdout, _ io.Writer) error {
		if gotExecutable != executable {
			t.Fatalf("executable does not match discovered Codex")
		}
		identityHome := ""
		for _, candidate := range []string{workHome, personalHome} {
			if slices.Contains(environment, "CODEX_HOME="+candidate) {
				identityHome = candidate
				break
			}
		}
		if identityHome == "" {
			t.Fatal("fake Codex did not receive a registered Identity Home")
		}
		switch strings.Join(args, " ") {
		case "app-server --stdio":
			checks[identityHome]++
			account := `{"id":2,"result":{"account":null}}`
			if authenticated[identityHome] {
				account = `{"id":2,"result":{"account":{"type":"chatgpt"}}}`
			}
			_, err := io.WriteString(stdout, account+"\n")
			return err
		case "login --device-auth":
			loginCalls++
			authenticated[identityHome] = true
			return nil
		default:
			return io.ErrUnexpectedEOF
		}
	})
	resolvePaths := func(*string) (platform.Paths, error) { return paths, nil }
	resolver := launchTestResolver{candidate: launch.Candidate{Path: executable, Version: "0.1.2"}}
	openStore := func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
		return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	}
	newAuthenticator := func() profile.Authenticator { return authenticator }

	var expiredStdout, expiredStderr bytes.Buffer
	started := 0
	var launchedHomes []string
	newProcess := func(plan launch.Plan, _ io.Reader, _ io.Writer, _ io.Writer) (foregroundProcess, error) {
		started++
		launchedHomes = append(launchedHomes, plan.Environment["CODEX_HOME"])
		return &launchTestProcess{pid: 7000 + started}, nil
	}
	if code := runLaunchWithInputAndDependenciesAndOwnerOptionsAndAuthenticator(
		[]string{"work", "--"}, strings.NewReader(""), &expiredStdout, &expiredStderr,
		resolvePaths, resolver, openStore, newProcess, nil, newAuthenticator, platform.OwnerOptions{},
	); code != exitFailure || started != 0 || expiredStdout.Len() != 0 || !strings.Contains(expiredStderr.String(), apperrors.ProfileReauthenticationRequired) {
		t.Fatalf("expired launch = code:%d started:%d stdout:%q stderr:%q", code, started, expiredStdout.String(), expiredStderr.String())
	}

	var recoveryStdout, recoveryStderr bytes.Buffer
	if code := runProfileWithInputAndDependenciesAndOwnerOptions(
		[]string{"reauthenticate", "work", "--device-code", "--non-interactive", "--json"}, strings.NewReader(""), &recoveryStdout, &recoveryStderr,
		resolvePaths, resolver, openStore,
		func(string) (profile.ManagedHomeProvisioner, error) {
			t.Fatal("reauthentication must not provision a new Identity Home")
			return nil, nil
		}, newAuthenticator, nil, platform.OwnerOptions{},
	); code != exitSuccess || recoveryStderr.Len() != 0 || strings.Contains(recoveryStdout.String(), filepath.Join(paths.Root, "managed-home")) {
		t.Fatalf("recovery = code:%d stdout:%q stderr:%q, want safe success", code, recoveryStdout.String(), recoveryStderr.String())
	}

	for _, alias := range []string{"work", "personal"} {
		for attempt := 0; attempt < 2; attempt++ {
			var stdout, stderr bytes.Buffer
			if code := runLaunchWithInputAndDependenciesAndOwnerOptionsAndAuthenticator(
				[]string{alias, "--"}, strings.NewReader(""), &stdout, &stderr,
				resolvePaths, resolver, openStore, newProcess, nil, newAuthenticator, platform.OwnerOptions{},
			); code != exitSuccess || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("%s launch %d = code:%d stdout:%q stderr:%q", alias, attempt+1, code, stdout.String(), stderr.String())
			}
		}
	}
	if want := []string{workHome, workHome, personalHome, personalHome}; !slices.Equal(launchedHomes, want) {
		t.Fatalf("foreground Identity Homes = %v, want %v", launchedHomes, want)
	}
	if started != 4 || loginCalls != 1 || checks[workHome] != 5 || checks[personalHome] != 2 {
		t.Fatalf("foreground starts/login/checks = %d/%d/%v, want four launches, one recovery, and reusable authentication", started, loginCalls, checks)
	}
	marker := filepath.Join(personalHome, "external-state")
	if err := os.WriteFile(marker, []byte("owned by user"), 0o600); err != nil {
		t.Fatalf("WriteFile(referenced marker) error = %v", err)
	}
	var removeStdout, removeStderr bytes.Buffer
	if code := runProfileWithInputAndDependenciesAndOwnerOptions(
		[]string{"remove", "personal", "--confirm", "Personal", "--non-interactive"}, strings.NewReader(""), &removeStdout, &removeStderr,
		resolvePaths, resolver, openStore, nil, nil, nil, platform.OwnerOptions{},
	); code != exitSuccess || removeStderr.Len() != 0 {
		t.Fatalf("referenced removal = code:%d stdout:%q stderr:%q", code, removeStdout.String(), removeStderr.String())
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "owned by user" {
		t.Fatalf("referenced content after removal = %q/%v, want untouched external state", content, err)
	}
}

func TestLaunchCLIMarksExistingProfileUnavailableWhenDiscoveryFails(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	started := false
	var stdout, stderr bytes.Buffer
	resultCode := runLaunchWithInputAndDependenciesAndOwnerOptionsAndAuthenticator(
		[]string{"work", "--"}, strings.NewReader(""), &stdout, &stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		launchTestResolver{},
		func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
			return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
		},
		func(launch.Plan, io.Reader, io.Writer, io.Writer) (foregroundProcess, error) {
			started = true
			return nil, errors.New("Codex must not start when discovery fails")
		},
		nil,
		func() profile.Authenticator { return &cliProfileAuthenticator{} },
		platform.OwnerOptions{},
	)
	if resultCode != exitFailure || started || stdout.Len() != 0 || !strings.Contains(stderr.String(), apperrors.LaunchCodexPathInvalid) {
		t.Fatalf("exit/started/stdout/stderr = %d/%t/%q/%q, want discovery failure before process start", resultCode, started, stdout.String(), stderr.String())
	}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("reopen store error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	item, err := stateStore.GetProfile(context.Background(), "work")
	if err != nil {
		t.Fatalf("GetProfile() error = %v", err)
	}
	if item.Status != profile.StatusUnavailable {
		t.Fatalf("profile status = %q, want unavailable", item.Status)
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
	tempRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(temp dir): %v", err)
	}
	root := filepath.Join(tempRoot, "state")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll(state) error = %v", err)
	}
	return platform.Paths{
		Root:              root,
		Runtime:           filepath.Join(root, "runtime"),
		LockFile:          filepath.Join(root, "runtime", "service.owner.lock"),
		MetadataFile:      filepath.Join(root, "runtime", "service.owner.json"),
		DatabaseFile:      filepath.Join(root, "codex-folio.sqlite3"),
		VaultFile:         filepath.Join(root, "codex-folio.vault"),
		ManagedHomes:      filepath.Join(root, "managed-homes"),
		ProfileQuarantine: filepath.Join(root, "profile-quarantine"),
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

func seedReferencedReadyProfile(t *testing.T, paths platform.Paths, secureVault vault.Vault) string {
	t.Helper()
	home := filepath.Join(filepath.Dir(paths.Root), "referenced-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("MkdirAll(referenced home) error = %v", err)
	}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatalf("open store error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	ctx := context.Background()
	if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: "profile-2", Alias: "Personal", DisplayName: "Personal"}); err != nil {
		t.Fatalf("CreatePendingProfile() error = %v", err)
	}
	if err := stateStore.SetReferencedHome(ctx, "profile-2", "profile-2", home); err != nil {
		t.Fatalf("SetReferencedHome() error = %v", err)
	}
	for _, stage := range []profile.SetupStage{profile.StageDiscovery, profile.StageHome, profile.StageAuthentication, profile.StageValidation} {
		if err := stateStore.SaveSetupStage(ctx, "profile-2", stage); err != nil {
			t.Fatalf("SaveSetupStage(%s) error = %v", stage, err)
		}
	}
	if _, err := stateStore.PromotePendingProfile(ctx, "profile-2"); err != nil {
		t.Fatalf("PromotePendingProfile() error = %v", err)
	}
	return home
}
