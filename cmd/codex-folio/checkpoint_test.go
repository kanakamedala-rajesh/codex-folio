package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	gitadapter "venkatasudha.com/codex-folio/internal/adapters/git"
	"venkatasudha.com/codex-folio/internal/continuation"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/vault"
)

func TestParseCheckpointCaptureAndShow(t *testing.T) {
	request, options, err := parseCheckpointRequest([]string{
		"capture", "/repo", "--goal", "ship", "--validation-command=go test ./...", "--validation-exit", "0",
		"--validation-at", "2026-09-08T12:00:00Z", "--validation-source", "ci", "--validation-freshness", "stale", "--redact-path", "secret.txt", "--redact-text=token", "--json",
		"--project-command", "go test ./...",
	})
	if err != nil || !options.json || request.Path != "/repo" || request.Goal != "ship" || request.Validation == nil || request.Validation.ExitStatus == nil || *request.Validation.ExitStatus != 0 || request.Validation.Source != "ci" || len(request.ProjectCommands) != 1 || len(request.RedactPaths) != 1 || len(request.RedactText) != 1 {
		t.Fatalf("capture = %#v/%#v, %v", request, options, err)
	}
	show, _, err := parseCheckpointRequest([]string{"show", "checkpoint-1"})
	if err != nil || show.ID != "checkpoint-1" {
		t.Fatalf("show = %#v, %v", show, err)
	}
	review, reviewOptions, err := parseCheckpointRequest([]string{"review", "checkpoint-1", "--redact-text", "secret", "--non-interactive"})
	if err != nil || review.ID != "checkpoint-1" || !reviewOptions.nonInteractive || len(review.RedactText) != 1 {
		t.Fatalf("review = %#v/%#v, %v", review, reviewOptions, err)
	}
	for _, invalid := range [][]string{{"capture", "a", "b"}, {"show"}, {"show", "id", "--goal", "no"}, {"show", "id", "--project-command", "go test"}, {"capture", "--validation-exit", "0"}, {"capture", "--validation-command", "go test", "--validation-freshness", "recent"}} {
		if _, _, err := parseCheckpointRequest(invalid); err == nil {
			t.Fatalf("parseCheckpointRequest(%q) error = nil", invalid)
		}
	}
	if code := runCheckpoint([]string{"show", "id", "--project-command", "go test"}, &bytes.Buffer{}, &bytes.Buffer{}, nil); code != exitUsage {
		t.Fatalf("show with capture-only option exit code = %d, want %d", code, exitUsage)
	}
}

func TestParseCheckpointRetention(t *testing.T) {
	request, options, err := parseCheckpointRequest([]string{"retention", "repository-first", "1", "--json"})
	if err != nil || !options.json || request.Source != continuation.SourceRepositoryFirst || request.Setting != "1" {
		t.Fatalf("retention = %#v/%#v, %v", request, options, err)
	}
	request, _, err = parseCheckpointRequest([]string{"retention"})
	if err != nil || request.Action != "retention" || request.Source != "" || request.Setting != "" {
		t.Fatalf("retention query = %#v, %v", request, err)
	}
	for _, invalid := range [][]string{{"retention", "repository-first"}, {"retention", "other", "1"}, {"retention", "transcript-assisted", "0"}} {
		if _, _, err := parseCheckpointRequest(invalid); err == nil {
			t.Fatalf("parseCheckpointRequest(%q) error = nil", invalid)
		}
	}
}

func TestParseCheckpointExport(t *testing.T) {
	request, options, err := parseCheckpointRequest([]string{"export", "checkpoint-1", "portable checkpoint.json", "--plaintext", "--yes"})
	if err != nil || request.ID != "checkpoint-1" || options.output != "portable checkpoint.json" || !options.plaintext || !options.yes {
		t.Fatalf("export = %#v/%#v, %v", request, options, err)
	}
	for _, invalid := range [][]string{{"export"}, {"export", "checkpoint-1"}, {"export", "checkpoint-1", "out", "extra"}, {"export", "checkpoint-1", "out.json"}, {"export", "checkpoint-1", "out.cfolio", "--plaintext"}, {"export", "checkpoint-1", "out.cfolio", "--redact-text", "secret"}} {
		if _, _, err := parseCheckpointRequest(invalid); err == nil {
			t.Fatalf("parseCheckpointRequest(%q) error = nil", invalid)
		}
	}
}

func TestWriteCheckpointExportPreservesExistingDestination(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "checkpoint.cfolio")
	if err := os.WriteFile(destination, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeCheckpointExport(destination, []byte("replacement")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("writeCheckpointExport() error = %v, want os.ErrExist", err)
	}
	kept, err := os.ReadFile(destination)
	if err != nil || string(kept) != "keep" {
		t.Fatalf("destination = %q, %v", kept, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("directory after failed export = %#v, %v", entries, err)
	}
}

func TestPlaintextCheckpointExportRequiresCompletePreview(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "checkpoint.json")
	err := exportCheckpoint(bufio.NewReader(strings.NewReader("export plaintext\n")), io.Discard, shortWriter{}, continuation.CheckpointExport{}, checkpointOptions{plaintext: true, output: destination})
	if err == nil {
		t.Fatal("exportCheckpoint() error = nil")
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination after incomplete preview: %v", statErr)
	}
}

func TestWritePlaintextCheckpointExportPreservesExistingDestination(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "checkpoint.json")
	if err := writePlaintextCheckpointExport(destination, []byte("plaintext")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(destination) {
		t.Fatalf("plaintext export files = %#v, %v", entries, err)
	}
	if err := os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writePlaintextCheckpointExport(destination, []byte("replacement")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("writePlaintextCheckpointExport() error = %v, want os.ErrExist", err)
	}
	kept, err := os.ReadFile(destination)
	if err != nil || string(kept) != "keep" {
		t.Fatalf("destination = %q, %v", kept, err)
	}
	entries, err = os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("directory after failed plaintext export = %#v, %v", entries, err)
	}
}

type shortWriter struct{}

func (shortWriter) Write(value []byte) (int, error) {
	return len(value) - 1, nil
}

func TestPartialValidationUsesExplicitUnknowns(t *testing.T) {
	request, _, err := parseCheckpointRequest([]string{"capture", "/repo", "--validation-command", "go test ./..."})
	if err != nil {
		t.Fatal(err)
	}
	if request.Validation.Source != continuation.ProvenanceUnknown || request.Validation.Freshness != continuation.FreshnessUnknown {
		t.Fatalf("validation = %#v", request.Validation)
	}
	second := continuation.ValidationEvidence{Command: "go vet ./...", Source: "local", Freshness: continuation.FreshnessFresh}
	if got := validationValue([]continuation.ValidationEvidence{*request.Validation, second}); got != "go test ./... (source unknown, freshness unknown, at unknown, exit unknown); go vet ./... (source local, freshness fresh, at unknown, exit unknown)" {
		t.Fatalf("validationValue() = %q", got)
	}
}

func TestEditCheckpointFilePassesEditorArgumentsAndPath(t *testing.T) {
	if os.Getenv("GO_WANT_CHECKPOINT_EDITOR_HELPER") == "1" {
		args := os.Args
		if len(args) < 2 || args[len(args)-2] != "--" {
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv("CHECKPOINT_EDITOR_MARKER"), []byte(args[len(args)-1]), 0o600); err != nil {
			os.Exit(3)
		}
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(4)
		}
		_, _ = os.Stdout.Write(input)
		_, _ = os.Stderr.Write([]byte("editor stderr"))
		os.Exit(0)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "editor-marker")
	checkpointPath := filepath.Join(t.TempDir(), "checkpoint with spaces.json")
	t.Setenv("GO_WANT_CHECKPOINT_EDITOR_HELPER", "1")
	t.Setenv("CHECKPOINT_EDITOR_MARKER", marker)
	t.Setenv("EDITOR", strconv.Quote(executable)+" -test.run=^TestEditCheckpointFilePassesEditorArgumentsAndPath$ --")
	var stdout, stderr bytes.Buffer
	if err := editCheckpointFile(checkpointPath, strings.NewReader("editor stdin"), &stdout, &stderr); err != nil {
		t.Fatalf("editCheckpointFile() error = %v", err)
	}
	got, err := os.ReadFile(marker)
	if err != nil || string(got) != checkpointPath {
		t.Fatalf("editor path = %q, %v; want %q", got, err, checkpointPath)
	}
	if stdout.String() != "editor stdin" || stderr.String() != "editor stderr" {
		t.Fatalf("editor streams = stdout %q stderr %q", stdout.String(), stderr.String())
	}
}

func TestCheckpointCLICapturesAndShowsThroughServiceAndEncryptedStore(t *testing.T) {
	paths := launchTestPaths(t)
	repository := filepath.Join(filepath.Dir(paths.Root), "repository")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	runCheckpointGit(t, repository, "init")
	runCheckpointGit(t, repository, "remote", "add", "origin", "https://user:credential-sentinel@example.invalid/repo")
	if err := os.WriteFile(filepath.Join(repository, "notes.txt"), []byte("raw prompt sentinel\nraw response sentinel\ntool output sentinel\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHECKPOINT_SECRET", "environment-value-sentinel")
	statusBefore := runCheckpointGit(t, repository, "status", "--porcelain=v1", "-z", "--untracked-files=all")

	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	secureVault, err := vault.NewMemoryVault(vault.MemoryVaultOptions{Key: bytes.Repeat([]byte{0x5c}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	if err != nil {
		t.Fatal(err)
	}
	projects, err := activity.NewProjectService(activity.ProjectServiceOptions{Repository: stateStore, Paths: platform.NewProjectPaths()})
	if err != nil {
		t.Fatal(err)
	}
	service, err := continuation.NewService(continuation.ServiceOptions{
		Repository: stateStore, Projects: projects, Inspector: gitadapter.NewInspector(),
		Now: func() time.Time { return time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC) }, HomeDirectory: filepath.Dir(repository),
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := httpapi.NewServer(httpapi.Options{Checkpoints: service, CommandToken: "checkpoint-token"})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: "checkpoint-token"}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runCheckpoint([]string{"retention", "repository-first", "1", "--json"}, &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil })
	var retention continuation.RetentionPolicy
	if decodeErr := json.Unmarshal(stdout.Bytes(), &retention); code != exitSuccess || stderr.Len() != 0 || decodeErr != nil || retention.RepositoryFirst != "1" || retention.TranscriptAssisted != "7" {
		t.Fatalf("retention CLI = code %d stdout %q stderr %q policy %#v error %v", code, stdout.String(), stderr.String(), retention, decodeErr)
	}
	stdout.Reset()
	code = runCheckpoint([]string{"capture", repository, "--goal", "finish " + filepath.Dir(repository) + " without token-value", "--project-command", "touch project-command-ran", "--redact-text", "token-value", "--json"}, &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil })
	var captured continuation.Checkpoint
	if decodeErr := json.Unmarshal(stdout.Bytes(), &captured); code != exitSuccess || stderr.Len() != 0 || decodeErr != nil || captured.ID == "" || captured.Project.Alias != "repository" || captured.Fields.Goal.Value != "finish [HOME] without [REDACTED]" || len(captured.Repository.ConfiguredCommands.Value) != 1 || captured.Retention != "1" || captured.ExpiresAt == nil || !captured.ExpiresAt.Equal(time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("capture CLI = code %d stdout %q stderr %q checkpoint %#v error %v", code, stdout.String(), stderr.String(), captured, decodeErr)
	}
	stdout.Reset()
	code = runCheckpoint([]string{"show", captured.ID, "--json"}, &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil })
	var shown continuation.Checkpoint
	if decodeErr := json.Unmarshal(stdout.Bytes(), &shown); code != exitSuccess || decodeErr != nil || shown.ID != captured.ID || shown.Fields.Goal.Value != captured.Fields.Goal.Value {
		t.Fatalf("show CLI = code %d stdout %q checkpoint %#v error %v", code, stdout.String(), shown, decodeErr)
	}

	stdout.Reset()
	stderr.Reset()
	var editedPath string
	editor := func(path string) error {
		editedPath = path
		if filepath.Clean(filepath.Dir(path)) == filepath.Clean(repository) {
			t.Fatalf("editor material was created in the repository: %s", path)
		}
		if info, statErr := os.Stat(path); statErr != nil {
			t.Fatal(statErr)
		} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("editor material mode = %o, want 600", info.Mode().Perm())
		}
		encoded, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		var fields continuation.CheckpointFields
		if decodeErr := json.Unmarshal(encoded, &fields); decodeErr != nil {
			return decodeErr
		}
		fields.Goal.Value = "approved token-value"
		fields.NextAction.Value = "run nothing"
		return os.WriteFile(path, mustJSON(t, fields), 0o600)
	}
	code = runCheckpointWithDependencies([]string{"review", captured.ID, "--redact-text", "token-value", "--redact-path", "notes.txt"}, strings.NewReader("approve\n"), &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil }, editor)
	if code != exitSuccess || !strings.Contains(stdout.String(), "Approved checkpoint:") || !strings.Contains(stdout.String(), "approved [REDACTED]") || !strings.Contains(stdout.String(), continuation.StatusApproved) {
		t.Fatalf("review CLI = code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(editedPath); !os.IsNotExist(err) {
		t.Fatalf("editor material was not cleaned up: %v", err)
	}

	encryptedPath := filepath.Join(repository, "portable checkpoint.cfolio")
	stdout.Reset()
	stderr.Reset()
	code = runCheckpointWithDependencies([]string{"export", captured.ID, encryptedPath}, strings.NewReader("portable passphrase\nportable passphrase\n"), &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil }, nil)
	artifact, readErr := os.ReadFile(encryptedPath)
	opened, openErr := continuation.OpenCheckpointExport(artifact, "portable passphrase")
	if code != exitSuccess || readErr != nil || openErr != nil || opened.Checkpoint.ID != captured.ID || opened.Checkpoint.Project.Alias != "repository" || opened.Checkpoint.Fields.Goal.Value != "approved [REDACTED]" || !strings.Contains(stderr.String(), "Checkpoint export preview:") {
		t.Fatalf("encrypted export = code %d stdout %q stderr %q read %v open %#v/%v", code, stdout.String(), stderr.String(), readErr, opened, openErr)
	}
	if bytes.Contains(artifact, []byte("approved [REDACTED]")) || bytes.Contains(artifact, []byte("portable passphrase")) {
		t.Fatalf("encrypted artifact contains protected material: %s", artifact)
	}
	if info, statErr := os.Stat(encryptedPath); statErr != nil {
		t.Fatal(statErr)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("encrypted export mode = %o, want 600", info.Mode().Perm())
	}
	if err := os.Remove(encryptedPath); err != nil {
		t.Fatal(err)
	}
	mismatchPath := filepath.Join(filepath.Dir(paths.Root), "mismatch.cfolio")
	code = runCheckpointWithDependencies([]string{"export", captured.ID, mismatchPath}, strings.NewReader("first passphrase\nsecond passphrase\n"), &bytes.Buffer{}, &bytes.Buffer{}, func(*string) (platform.Paths, error) { return paths, nil }, nil)
	if _, statErr := os.Stat(mismatchPath); code == exitSuccess || !os.IsNotExist(statErr) {
		t.Fatalf("mismatched passphrase = code %d stat %v", code, statErr)
	}

	plaintextPath := filepath.Join(filepath.Dir(paths.Root), "plain checkpoint.json")
	stdout.Reset()
	stderr.Reset()
	code = runCheckpointWithDependencies([]string{"export", captured.ID, plaintextPath, "--plaintext", "--yes"}, strings.NewReader("cancel\n"), &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil }, nil)
	_, statErr := os.Stat(plaintextPath)
	if code == exitSuccess || !os.IsNotExist(statErr) || !strings.Contains(stderr.String(), `"format_version": "codex-folio.checkpoint.v1"`) {
		t.Fatalf("plaintext refusal = code %d stderr %q stat %v", code, stderr.String(), statErr)
	}
	stdout.Reset()
	stderr.Reset()
	code = runCheckpointWithDependencies([]string{"export", captured.ID, plaintextPath, "--plaintext", "--yes"}, strings.NewReader("export plaintext\n"), &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil }, nil)
	plain, readErr := os.ReadFile(plaintextPath)
	var plainExport continuation.CheckpointExport
	decodeErr := json.Unmarshal(plain, &plainExport)
	if code != exitSuccess || readErr != nil || decodeErr != nil || plainExport.Checkpoint.ID != captured.ID {
		t.Fatalf("plaintext export = code %d stdout %q stderr %q export %#v errors %v/%v", code, stdout.String(), stderr.String(), plainExport, readErr, decodeErr)
	}
	for _, forbidden := range []string{repository, "raw prompt sentinel", "raw response sentinel", "tool output sentinel", "environment-value-sentinel", "credential-sentinel", "token-value", `"canonical_path"`, `"identity_home"`, `"raw_transcript"`, `"raw_diff"`} {
		if bytes.Contains(plain, []byte(forbidden)) {
			t.Fatalf("plaintext export contains excluded content %q: %s", forbidden, plain)
		}
	}
	if code := runCheckpointWithDependencies([]string{"export", captured.ID, filepath.Join(t.TempDir(), "blocked.cfolio"), "--non-interactive"}, strings.NewReader("portable passphrase\nportable passphrase\n"), &bytes.Buffer{}, &bytes.Buffer{}, func(*string) (platform.Paths, error) { return paths, nil }, nil); code == exitSuccess {
		t.Fatal("non-interactive export succeeded")
	}

	stdout.Reset()
	stderr.Reset()
	code = runCheckpointWithDependencies([]string{"review", captured.ID}, strings.NewReader("cancel\n"), &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil }, func(path string) error {
		var fields continuation.CheckpointFields
		encoded, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if decodeErr := json.Unmarshal(encoded, &fields); decodeErr != nil {
			return decodeErr
		}
		fields.Goal.Value = "cancelled edit"
		return os.WriteFile(path, mustJSON(t, fields), 0o600)
	})
	if code != exitSuccess || !strings.Contains(stdout.String(), "Checkpoint remains draft.") {
		t.Fatalf("cancel review = code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	stored, err := service.Show(t.Context(), captured.ID)
	if err != nil || stored.Status != continuation.StatusDraft || stored.Fields.Goal.Value != "cancelled edit" {
		t.Fatalf("cancelled stored checkpoint = %#v, %v", stored, err)
	}

	editorCalled := false
	code = runCheckpointWithDependencies([]string{"review", captured.ID, "--non-interactive"}, strings.NewReader("approve\n"), &bytes.Buffer{}, &bytes.Buffer{}, func(*string) (platform.Paths, error) { return paths, nil }, func(string) error { editorCalled = true; return nil })
	if code == exitSuccess || editorCalled {
		t.Fatalf("non-interactive review = code %d editor called %t", code, editorCalled)
	}
	code = runCheckpointWithDependencies([]string{"review", captured.ID}, strings.NewReader("approve\n"), &bytes.Buffer{}, &bytes.Buffer{}, func(*string) (platform.Paths, error) { return paths, nil }, func(string) error { return errors.New("editor failed") })
	if code == exitSuccess {
		t.Fatal("editor failure approved checkpoint")
	}
	code = runCheckpointWithDependencies([]string{"review", captured.ID}, strings.NewReader("approve\n"), &bytes.Buffer{}, &bytes.Buffer{}, func(*string) (platform.Paths, error) { return paths, nil }, func(path string) error {
		return os.WriteFile(path, []byte(`{"goal":`), 0o600)
	})
	if code == exitSuccess {
		t.Fatal("invalid edited content approved checkpoint")
	}
	code = runCheckpointWithDependencies([]string{"review", captured.ID}, strings.NewReader("approve\n"), &bytes.Buffer{}, &bytes.Buffer{}, func(*string) (platform.Paths, error) { return paths, nil }, func(path string) error {
		fields := stored.Fields
		fields.Goal.Value = strings.Repeat("x", continuation.MaxCheckpointBytes)
		return os.WriteFile(path, mustJSON(t, fields), 0o600)
	})
	if code == exitSuccess {
		t.Fatal("oversize edited content approved checkpoint")
	}
	statusAfter := runCheckpointGit(t, repository, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if string(statusBefore) != string(statusAfter) {
		t.Fatalf("repository changed during capture: before %q after %q", statusBefore, statusAfter)
	}
	if _, err := os.Stat(filepath.Join(repository, "project-command-ran")); !os.IsNotExist(err) {
		t.Fatalf("configured project command was executed: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := os.ReadFile(paths.DatabaseFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{repository, "raw prompt sentinel", "raw response sentinel", "tool output sentinel", "environment-value-sentinel", "credential-sentinel", "token-value"} {
		if bytes.Contains(database, []byte(forbidden)) {
			t.Fatalf("database contains excluded content %q", forbidden)
		}
	}
}

func runCheckpointGit(t *testing.T, directory string, args ...string) []byte {
	t.Helper()
	output, err := exec.Command("git", append([]string{"-C", directory}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return output
}
