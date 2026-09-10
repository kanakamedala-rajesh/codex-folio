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
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	"venkatasudha.com/codex-folio/internal/continuation"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/usage"
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
	reader := bufferedReader(strings.NewReader("1\napprove\n"))
	code := runSafeContinuationOffer(23, &launch.SafeContinuationOffer{Alternatives: []launch.SafeContinuationAlternative{{Alias: "Personal", CapacityState: usage.FreshnessFresh, Provenance: usage.ProvenanceProvider, Recommended: true}}}, reader, &stdout, &stderr, func(target string) int {
		return runHandoffWithDependencies(
			[]string{target, repository, "--goal", "draft goal", "--next-action", "continue"}, reader, &stdout, &stderr,
			resolvePaths, resolver, openStore,
			func(got launch.Plan, _ io.Reader, _, _ io.Writer) (foregroundProcess, error) {
				plan = got
				return process, nil
			}, nil,
			editor, authenticator, platform.OwnerOptions{},
		)
	})
	if code != 130 || !process.started || stderr.String() != "Choose an alternative to review a repository-first checkpoint, or press Enter to decline: Type 'approve' to approve this sanitized revision; anything else cancels: " {
		t.Fatalf("handoff = code:%d started:%t stdout:%q stderr:%q", code, process.started, stdout.String(), stderr.String())
	}
	if plan.WorkingDirectory != repository || plan.Environment["CODEX_HOME"] != filepath.Join(paths.Root, "personal-home") || len(plan.Arguments) != 1 {
		t.Fatalf("target plan = %#v", plan)
	}
	var supplied continuation.Checkpoint
	if err := json.Unmarshal([]byte(plan.Arguments[0]), &supplied); err != nil || supplied.Status != continuation.StatusApproved || supplied.Retention != continuation.DefaultRepositoryRetention || supplied.Fields.Goal.Value != "approved goal" || strings.Contains(plan.Arguments[0], "draft goal") {
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

func TestHandoffCLIUsesConsentedHistoryOnlyAfterSanitizedApproval(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	seedSecondReadyProfile(t, paths, secureVault)
	repository := filepath.Join(filepath.Dir(paths.Root), "history-handoff-repository")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	runCheckpointGit(t, repository, "init")
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
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), source.LeaseID, 5201); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchExited(context.Background(), source.LeaseID, 1); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}

	threadID := "018f4f70-6f77-7c3f-9b77-93aa087dfc4d"
	historyCalls := 0
	var historyHome, historyInput string
	history := codexadapter.NewHistoryReaderWithCommandRunner(func(_ context.Context, _ string, _ []string, environment []string, input io.Reader, output, _ io.Writer) error {
		historyCalls++
		for _, entry := range environment {
			name, value, found := strings.Cut(entry, "=")
			if found && strings.EqualFold(name, "CODEX_HOME") {
				historyHome = value
			}
		}
		encoded, err := io.ReadAll(input)
		if err != nil {
			return err
		}
		historyInput = string(encoded)
		_, err = io.WriteString(output, `{"id":4,"result":{"thread":{"id":"018f4f70-6f77-7c3f-9b77-93aa087dfc4d","status":{"type":"notLoaded"},"turns":[{"items":[{"type":"userMessage","content":[{"type":"text","text":"raw prompt sentinel\nGoal: candidate secret-sentinel goal\nNext action: candidate next"}]},{"type":"commandExecution","aggregatedOutput":"credential sentinel"},{"type":"agentMessage","text":"raw response sentinel\nCompleted work: candidate completed"}]}]}}}`)
		return err
	})
	var plan launch.Plan
	resolvePaths := func(*string) (platform.Paths, error) { return paths, nil }
	openStore := func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
		return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	}
	editor := func(string, io.Reader, io.Writer, io.Writer) error {
		t.Fatal("transcript candidates must not be passed to the file editor")
		return nil
	}
	process := &launchTestProcess{pid: 5202, exitStatus: 0}
	var assistedOutput, assistedErrors strings.Builder
	code := runHandoffWithHistoryDependencies(
		[]string{"Personal", repository, "--history", threadID, "--redact-text", "secret-sentinel"}, strings.NewReader("assist\napproved secret-sentinel goal\n\n\n\n\napprove\n"), &assistedOutput, &assistedErrors,
		resolvePaths, launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "codex"), Version: "0.1.2"}}, openStore,
		func(got launch.Plan, _ io.Reader, _, _ io.Writer) (foregroundProcess, error) {
			plan = got
			return process, nil
		},
		nil, editor, func() profile.Authenticator { return &cliProfileAuthenticator{} }, platform.OwnerOptions{}, history,
	)
	if code != exitSuccess || historyCalls != 1 || historyHome != filepath.Join(paths.Root, "managed-home") || !strings.Contains(historyInput, `"threadId":"`+threadID+`"`) || !process.started {
		t.Fatalf("assisted handoff = code %d, calls %d, home %q, input %q, process %#v, stdout %q, stderr %q", code, historyCalls, historyHome, historyInput, process, assistedOutput.String(), assistedErrors.String())
	}
	var supplied continuation.Checkpoint
	if len(plan.Arguments) != 1 || json.Unmarshal([]byte(plan.Arguments[0]), &supplied) != nil || supplied.Source != continuation.SourceTranscriptAssisted || supplied.Status != continuation.StatusApproved || supplied.Retention != continuation.DefaultTranscriptRetention || supplied.Fields.Goal.Value != "approved [REDACTED] goal" || supplied.ExpiresAt.Sub(supplied.CreatedAt) != 7*24*time.Hour {
		t.Fatalf("supplied assisted checkpoint = %#v; plan %#v", supplied, plan)
	}
	for _, forbidden := range []string{"candidate secret-sentinel goal", "approved secret-sentinel goal", "raw prompt sentinel", "raw response sentinel", "credential sentinel"} {
		if strings.Contains(plan.Arguments[0], forbidden) {
			t.Fatalf("target context contains unapproved or unsanitized history %q", forbidden)
		}
		for _, path := range []string{paths.DatabaseFile, paths.DatabaseFile + "-wal"} {
			encoded, err := os.ReadFile(path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("persistent storage contains excluded history %q in %s", forbidden, path)
			}
		}
	}

	fallbackHistory := &historyReaderStub{err: errors.Join(errors.New("workspace policy disallowed"), continuation.ErrHistoryUnavailable)}
	fallbackEditor := func(path string, _ io.Reader, _, _ io.Writer) error {
		var fields continuation.CheckpointFields
		encoded, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(encoded, &fields); err != nil {
			return err
		}
		if fields.Goal.Value != "repository fallback" {
			t.Fatalf("fallback fields = %#v", fields)
		}
		fields.Goal.Value = "approved repository fallback"
		return os.WriteFile(path, mustJSON(t, fields), 0o600)
	}
	var fallbackPlan launch.Plan
	fallbackProcess := &launchTestProcess{pid: 5203, exitStatus: 0}
	code = runHandoffWithHistoryDependencies(
		[]string{"Work", repository, "--history", threadID, "--goal", "repository fallback"}, strings.NewReader("assist\napprove\n"), io.Discard, io.Discard,
		resolvePaths, launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "codex"), Version: "0.1.2"}}, openStore,
		func(got launch.Plan, _ io.Reader, _, _ io.Writer) (foregroundProcess, error) {
			fallbackPlan = got
			return fallbackProcess, nil
		},
		nil, fallbackEditor, func() profile.Authenticator { return &cliProfileAuthenticator{} }, platform.OwnerOptions{}, fallbackHistory,
	)
	var fallbackCheckpoint continuation.Checkpoint
	if code != exitSuccess || fallbackHistory.calls != 1 || json.Unmarshal([]byte(fallbackPlan.Arguments[0]), &fallbackCheckpoint) != nil || fallbackCheckpoint.Source != continuation.SourceRepositoryFirst || fallbackCheckpoint.Fields.Goal.Value != "approved repository fallback" {
		t.Fatalf("repository fallback = code %d, calls %d, checkpoint %#v", code, fallbackHistory.calls, fallbackCheckpoint)
	}

	noConsentHistory := &historyReaderStub{fields: continuation.CheckpointFields{Goal: continuation.Evidence[string]{Value: "must not be read"}}}
	noConsentEditor := func(path string, _ io.Reader, _, _ io.Writer) error {
		var fields continuation.CheckpointFields
		encoded, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(encoded, &fields); err != nil {
			return err
		}
		if fields.Goal.Value != "no consent" {
			t.Fatalf("no-consent fields = %#v", fields)
		}
		return os.WriteFile(path, encoded, 0o600)
	}
	var noConsentPlan launch.Plan
	code = runHandoffWithHistoryDependencies(
		[]string{"Personal", repository, "--history", threadID, "--goal", "no consent"}, strings.NewReader("repository-only\napprove\n"), io.Discard, io.Discard,
		resolvePaths, launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "codex"), Version: "0.1.2"}}, openStore,
		func(got launch.Plan, _ io.Reader, _, _ io.Writer) (foregroundProcess, error) {
			noConsentPlan = got
			return &launchTestProcess{pid: 5204, exitStatus: 0}, nil
		},
		nil, noConsentEditor, func() profile.Authenticator { return &cliProfileAuthenticator{} }, platform.OwnerOptions{}, noConsentHistory,
	)
	var noConsentCheckpoint continuation.Checkpoint
	if code != exitSuccess || noConsentHistory.calls != 0 || json.Unmarshal([]byte(noConsentPlan.Arguments[0]), &noConsentCheckpoint) != nil || noConsentCheckpoint.Source != continuation.SourceRepositoryFirst {
		t.Fatalf("no-consent fallback = code %d, calls %d, checkpoint %#v", code, noConsentHistory.calls, noConsentCheckpoint)
	}

	cancelledHistory := &historyReaderStub{fields: continuation.CheckpointFields{Goal: continuation.Evidence[string]{Value: "cancelled history sentinel"}}}
	cancelledStart := false
	var cancelledOutput strings.Builder
	code = runHandoffWithHistoryDependencies(
		[]string{"Work", repository, "--history", threadID, "--goal", "cancelled repository draft"}, strings.NewReader("assist\n\n\n\n\n\ncancel\n"), &cancelledOutput, io.Discard,
		resolvePaths, launchTestResolver{candidate: launch.Candidate{Path: filepath.Join(paths.Root, "codex"), Version: "0.1.2"}}, openStore,
		func(launch.Plan, io.Reader, io.Writer, io.Writer) (foregroundProcess, error) {
			cancelledStart = true
			return nil, nil
		},
		nil, func(string, io.Reader, io.Writer, io.Writer) error {
			t.Fatal("cancelled transcript candidates must not be passed to the file editor")
			return nil
		}, func() profile.Authenticator { return &cliProfileAuthenticator{} }, platform.OwnerOptions{}, cancelledHistory,
	)
	if code != exitSuccess || cancelledHistory.calls != 1 || cancelledStart || !strings.Contains(cancelledOutput.String(), "Checkpoint remains repository-first draft.") {
		t.Fatalf("assisted cancellation = code %d, calls %d, started %t, output %q", code, cancelledHistory.calls, cancelledStart, cancelledOutput.String())
	}
	for _, path := range []string{paths.DatabaseFile, paths.DatabaseFile + "-wal"} {
		encoded, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "cancelled history sentinel") {
			t.Fatalf("cancelled history candidate persisted in %s", path)
		}
	}
}

type historyReaderStub struct {
	request continuation.HistoryReadRequest
	fields  continuation.CheckpointFields
	err     error
	calls   int
}

func (stub *historyReaderStub) Read(_ context.Context, request continuation.HistoryReadRequest) (continuation.CheckpointFields, error) {
	stub.calls++
	stub.request = request
	return stub.fields, stub.err
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
	threadID := "018f4f70-6f77-7c3f-9b77-93aa087dfc4d"
	target, capture, options, err := parseHandoffArguments([]string{"Personal", "/repo", "--goal", "continue", "--history", threadID, "--codex-bin", "/opt/codex", "--state-root=/state"})
	if err != nil || target != "Personal" || capture.Path != "/repo" || capture.Goal != "continue" || options.codexBin != "/opt/codex" || options.historyThreadID != threadID || options.stateRoot == nil || *options.stateRoot != "/state" {
		t.Fatalf("parsed handoff = %q/%#v/%#v, %v", target, capture, options, err)
	}
	for _, args := range [][]string{{}, {"Personal", "--non-interactive"}, {"Personal", "--json"}, {"Personal", "--codex-bin="}, {"Personal", "--history="}, {"Personal", "--history", "not-a-thread"}, {"Personal", "--history", threadID, "--history", threadID}, {"Personal", "--", "resume", "thread"}} {
		if _, _, _, err := parseHandoffArguments(args); err == nil {
			t.Fatalf("parseHandoffArguments(%q) error = nil", args)
		}
	}
}
