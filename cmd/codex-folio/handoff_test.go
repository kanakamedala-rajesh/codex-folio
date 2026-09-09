package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/continuation"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
)

func TestHandoffCLIReviewsApprovedContextAndLaunchesFreshTargetInSourceRepository(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	seedSecondReadyProfile(t, paths, secureVault)
	repository := filepath.Join(filepath.Dir(paths.Root), "handoff-repository")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	runCheckpointGit(t, repository, "init")
	if err := os.WriteFile(filepath.Join(repository, "notes.txt"), []byte("preserve repository state\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workHome := filepath.Join(paths.Root, "managed-home")
	personalHome := filepath.Join(paths.Root, "personal-home")
	for _, home := range []string{workHome, personalHome} {
		if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte("credential fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	repositoryBefore := snapshotHandoffTree(t, repository, true)
	workHomeBefore := snapshotHandoffTree(t, workHome, false)
	personalHomeBefore := snapshotHandoffTree(t, personalHome, false)
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatal(err)
	}
	projects, err := activity.NewProjectService(activity.ProjectServiceOptions{Repository: stateStore, Paths: platform.NewProjectPaths()})
	if err != nil {
		t.Fatal(err)
	}
	project, err := projects.Resolve(context.Background(), repository, "Folio")
	if err != nil {
		t.Fatal(err)
	}
	source, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{Alias: "Work", Executable: filepath.Join(paths.Root, "codex"), WorkingDirectory: repository, ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), source.LeaseID, 5001); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchExited(context.Background(), source.LeaseID, 1); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}

	var plan launch.Plan
	process := &launchTestProcess{pid: 5002, exitStatus: 130, waitErr: context.Canceled}
	resolvePaths := func(*string) (platform.Paths, error) { return paths, nil }
	resolver := launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "codex"), Version: "0.1.2"}}
	openStore := func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
		return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	}
	editor := func(path string, _ io.Reader, _, _ io.Writer) error {
		var fields continuation.CheckpointFields
		encoded, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(encoded, &fields); err != nil {
			return err
		}
		fields.Goal.Value = "approved goal"
		return os.WriteFile(path, mustJSON(t, fields), 0o600)
	}
	authenticator := func() profile.Authenticator { return &cliProfileAuthenticator{} }
	var stdout, stderr bytes.Buffer
	cancelledStart := false
	if code := runHandoffWithDependencies(
		[]string{"Personal", repository, "--goal", "cancelled"}, strings.NewReader("cancel\n"), &stdout, &stderr,
		resolvePaths, resolver, openStore,
		func(launch.Plan, io.Reader, io.Writer, io.Writer) (foregroundProcess, error) {
			cancelledStart = true
			return process, nil
		},
		nil, editor, authenticator, platform.OwnerOptions{},
	); code != exitSuccess || cancelledStart || !strings.Contains(stdout.String(), "Checkpoint remains draft.") {
		t.Fatalf("cancelled handoff = code:%d started:%t stdout:%q stderr:%q", code, cancelledStart, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code := runHandoffWithDependencies(
		[]string{"Personal", repository, "--goal", "draft goal", "--next-action", "continue"}, strings.NewReader("approve\n"), &stdout, &stderr,
		resolvePaths, resolver, openStore,
		func(got launch.Plan, _ io.Reader, _, _ io.Writer) (foregroundProcess, error) {
			plan = got
			return process, nil
		}, nil,
		editor, authenticator, platform.OwnerOptions{},
	)
	if code != 130 || !process.started || stderr.String() != "Type 'approve' to approve this sanitized revision; anything else cancels: " {
		t.Fatalf("handoff = code:%d started:%t stdout:%q stderr:%q", code, process.started, stdout.String(), stderr.String())
	}
	if plan.WorkingDirectory != repository || plan.Environment["CODEX_HOME"] != filepath.Join(paths.Root, "personal-home") || len(plan.Arguments) != 1 {
		t.Fatalf("target plan = %#v", plan)
	}
	var supplied continuation.Checkpoint
	if err := json.Unmarshal([]byte(plan.Arguments[0]), &supplied); err != nil || supplied.Status != continuation.StatusApproved || supplied.Fields.Goal.Value != "approved goal" || strings.Contains(plan.Arguments[0], "draft goal") {
		t.Fatalf("supplied checkpoint = %#v, %v; context %q", supplied, err, plan.Arguments[0])
	}
	assertSelectedAlias(t, paths, secureVault, "Work")
	stateStore, err = store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := stateStore.LoadCheckpoint(context.Background(), supplied.ID)
	if err != nil || stored.Status != continuation.StatusCompleted || stored.ExpiresAt == nil {
		t.Fatalf("completed checkpoint = %#v, %v", stored, err)
	}
	launchRecord, err := stateStore.GetManagedLaunch(context.Background(), plan.LeaseID)
	if err != nil || launchRecord.State != launch.StateExited || launchRecord.ExitStatus == nil || *launchRecord.ExitStatus != 130 {
		t.Fatalf("interrupted target lifecycle = %#v, %v", launchRecord, err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}
	if got := snapshotHandoffTree(t, repository, true); !reflect.DeepEqual(got, repositoryBefore) {
		t.Fatalf("repository changed across fake handoff: before=%v after=%v", repositoryBefore, got)
	}
	if got := snapshotHandoffTree(t, workHome, false); !reflect.DeepEqual(got, workHomeBefore) {
		t.Fatalf("source Identity Home or credentials changed: before=%v after=%v", workHomeBefore, got)
	}
	if got := snapshotHandoffTree(t, personalHome, false); !reflect.DeepEqual(got, personalHomeBefore) {
		t.Fatalf("target Identity Home or credentials changed: before=%v after=%v", personalHomeBefore, got)
	}

	stateStore, err = openStore(paths, "", "")
	if err != nil {
		t.Fatal(err)
	}
	uncertainSource, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{Alias: "Personal", Executable: filepath.Join(paths.Root, "codex"), WorkingDirectory: repository, ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}
	started := false
	code = runHandoffWithDependencies(
		[]string{"Work", repository, "--goal", "source state uncertain"}, strings.NewReader("approve\n"), io.Discard, io.Discard,
		resolvePaths, resolver, openStore,
		func(launch.Plan, io.Reader, io.Writer, io.Writer) (foregroundProcess, error) {
			started = true
			return process, nil
		},
		nil, editor, authenticator, platform.OwnerOptions{},
	)
	if code != exitFailure || started {
		t.Fatalf("uncertain source = code:%d started:%t", code, started)
	}
	stateStore, err = openStore(paths, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), uncertainSource.LeaseID, 5003); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchExited(context.Background(), uncertainSource.LeaseID, 0); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}

	var failedPlan launch.Plan
	stdout.Reset()
	stderr.Reset()
	code = runHandoffWithDependencies(
		[]string{"Work", repository, "--goal", "retry"}, strings.NewReader("approve\n"), &stdout, &stderr,
		resolvePaths, resolver, openStore,
		func(got launch.Plan, _ io.Reader, _, _ io.Writer) (foregroundProcess, error) {
			failedPlan = got
			return nil, errors.New("fake Codex start failed")
		}, nil, editor, authenticator, platform.OwnerOptions{},
	)
	if code != exitFailure || failedPlan.LeaseID == "" {
		t.Fatalf("failed start = code:%d plan:%#v stdout:%q stderr:%q", code, failedPlan, stdout.String(), stderr.String())
	}
	var failedCheckpoint continuation.Checkpoint
	if err := json.Unmarshal([]byte(failedPlan.Arguments[0]), &failedCheckpoint); err != nil {
		t.Fatal(err)
	}
	stateStore, err = openStore(paths, "", "")
	if err != nil {
		t.Fatal(err)
	}
	recoverable, err := stateStore.LoadCheckpoint(context.Background(), failedCheckpoint.ID)
	if err != nil || recoverable.Status != continuation.StatusApproved || recoverable.ExpiresAt == nil {
		t.Fatalf("recoverable failed-start checkpoint = %#v, %v", recoverable, err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}

	stateStore, err = openStore(paths, "", "")
	if err != nil {
		t.Fatal(err)
	}
	runningSource, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{Alias: "Work", Executable: filepath.Join(paths.Root, "codex"), WorkingDirectory: repository, ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), runningSource.LeaseID, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}
	started = false
	code = runHandoffWithDependencies(
		[]string{"Personal", repository, "--goal", "source still running"}, strings.NewReader("approve\n"), io.Discard, io.Discard,
		resolvePaths, resolver, openStore,
		func(launch.Plan, io.Reader, io.Writer, io.Writer) (foregroundProcess, error) {
			started = true
			return process, nil
		},
		nil, editor, authenticator, platform.OwnerOptions{},
	)
	if code != exitFailure || started {
		t.Fatalf("running source = code:%d started:%t", code, started)
	}
	stateStore, err = openStore(paths, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchExited(context.Background(), runningSource.LeaseID, 0); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.Rename(filepath.Join(paths.Root, "personal-home"), filepath.Join(paths.Root, "personal-home-away")); err != nil {
		t.Fatal(err)
	}
	started = false
	code = runHandoffWithDependencies(
		[]string{"Personal", repository, "--goal", "unavailable target"}, strings.NewReader("approve\n"), io.Discard, io.Discard,
		resolvePaths, resolver, openStore,
		func(launch.Plan, io.Reader, io.Writer, io.Writer) (foregroundProcess, error) {
			started = true
			return process, nil
		},
		nil, editor, authenticator, platform.OwnerOptions{},
	)
	if code != exitFailure || started {
		t.Fatalf("unavailable target = code:%d started:%t", code, started)
	}
}

func snapshotHandoffTree(t *testing.T, root string, skipGit bool) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if skipGit && entry.IsDir() && relative == ".git" {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[relative] = string(content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestParseHandoffRequiresInteractiveCaptureWithoutCodexArguments(t *testing.T) {
	target, capture, options, err := parseHandoffArguments([]string{"Personal", "/repo", "--goal", "continue", "--codex-bin", "/opt/codex", "--state-root=/state"})
	if err != nil || target != "Personal" || capture.Path != "/repo" || capture.Goal != "continue" || options.codexBin != "/opt/codex" || options.stateRoot == nil || *options.stateRoot != "/state" {
		t.Fatalf("parsed handoff = %q/%#v/%#v, %v", target, capture, options, err)
	}
	for _, args := range [][]string{{}, {"Personal", "--non-interactive"}, {"Personal", "--json"}, {"Personal", "--codex-bin="}, {"Personal", "--", "resume", "thread"}} {
		if _, _, _, err := parseHandoffArguments(args); err == nil {
			t.Fatalf("parseHandoffArguments(%q) error = nil", args)
		}
	}
}
