package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
	for _, invalid := range [][]string{{"capture", "a", "b"}, {"show"}, {"show", "id", "--goal", "no"}, {"show", "id", "--project-command", "go test"}, {"capture", "--validation-exit", "0"}, {"capture", "--validation-command", "go test", "--validation-freshness", "recent"}} {
		if _, _, err := parseCheckpointRequest(invalid); err == nil {
			t.Fatalf("parseCheckpointRequest(%q) error = nil", invalid)
		}
	}
	if code := runCheckpoint([]string{"show", "id", "--project-command", "go test"}, &bytes.Buffer{}, &bytes.Buffer{}, nil); code != exitUsage {
		t.Fatalf("show with capture-only option exit code = %d, want %d", code, exitUsage)
	}
}

func TestPartialValidationUsesExplicitUnknowns(t *testing.T) {
	request, _, err := parseCheckpointRequest([]string{"capture", "/repo", "--validation-command", "go test ./..."})
	if err != nil {
		t.Fatal(err)
	}
	if request.Validation.Source != continuation.ProvenanceUnknown || request.Validation.Freshness != continuation.FreshnessUnknown {
		t.Fatalf("validation = %#v", request.Validation)
	}
	if got := validationValue([]continuation.ValidationEvidence{*request.Validation}); got != "go test ./... (source unknown, freshness unknown, at unknown, exit unknown)" {
		t.Fatalf("validationValue() = %q", got)
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
	code := runCheckpoint([]string{"capture", repository, "--goal", "finish " + filepath.Dir(repository) + " without token-value", "--project-command", "touch project-command-ran", "--redact-text", "token-value", "--json"}, &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil })
	var captured continuation.Checkpoint
	if decodeErr := json.Unmarshal(stdout.Bytes(), &captured); code != exitSuccess || stderr.Len() != 0 || decodeErr != nil || captured.ID == "" || captured.Project.Alias != "repository" || captured.Fields.Goal.Value != "finish [HOME] without [REDACTED]" || len(captured.Repository.ConfiguredCommands.Value) != 1 {
		t.Fatalf("capture CLI = code %d stdout %q stderr %q checkpoint %#v error %v", code, stdout.String(), stderr.String(), captured, decodeErr)
	}
	stdout.Reset()
	code = runCheckpoint([]string{"show", captured.ID, "--json"}, &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil })
	var shown continuation.Checkpoint
	if decodeErr := json.Unmarshal(stdout.Bytes(), &shown); code != exitSuccess || decodeErr != nil || shown.ID != captured.ID || shown.Fields.Goal.Value != captured.Fields.Goal.Value {
		t.Fatalf("show CLI = code %d stdout %q checkpoint %#v error %v", code, stdout.String(), shown, decodeErr)
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
