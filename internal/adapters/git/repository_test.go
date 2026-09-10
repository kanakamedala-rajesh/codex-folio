package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestInspectorReadsDirtyLinkedWorktreeWithoutMutation(t *testing.T) {
	root := t.TempDir()
	repository, worktree := filepath.Join(root, "repository"), filepath.Join(root, "linked worktree")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repository, "init")
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repository, "add", "tracked.txt")
	runTestGit(t, repository, "-c", "user.name=CodexFolio Test", "-c", "user.email=test@example.invalid", "commit", "-m", "fixture")
	runTestGit(t, repository, "worktree", "add", "-b", "checkpoint-worktree", worktree)
	if err := os.WriteFile(filepath.Join(worktree, "staged.txt"), []byte("staged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, worktree, "add", "staged.txt")
	if err := os.WriteFile(filepath.Join(worktree, "tracked.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "untracked file.txt"), []byte("untracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := runTestGit(t, worktree, "status", "--porcelain=v1", "-z", "--untracked-files=all")

	inventory, err := NewInspector().Inspect(context.Background(), worktree)
	if err != nil {
		t.Fatal(err)
	}
	after := runTestGit(t, worktree, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if string(before) != string(after) {
		t.Fatalf("repository status changed during inspection: before %q after %q", before, after)
	}
	if inventory.Branch != "checkpoint-worktree" || len(inventory.Staged) != 1 || len(inventory.Modified) != 1 || len(inventory.Untracked) != 1 || inventory.Diff.FilesChanged != 2 {
		t.Fatalf("worktree inventory = %#v", inventory)
	}
	if _, err := os.Stat(filepath.Join(worktree, ".codex-folio")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repository-local artifact exists: %v", err)
	}
}

func TestInspectorReportsNetDiffForUnbornRepository(t *testing.T) {
	repository := t.TempDir()
	runTestGit(t, repository, "init")
	path := filepath.Join(repository, "tracked.txt")
	if err := os.WriteFile(path, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repository, "add", "tracked.txt")
	if err := os.WriteFile(path, []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	inventory, err := NewInspector().Inspect(context.Background(), repository)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Diff.FilesChanged != 1 || inventory.Diff.Insertions != 1 || inventory.Diff.Deletions != 0 {
		t.Fatalf("diff = %#v", inventory.Diff)
	}
}

func TestRunGitDisablesOptionalLocks(t *testing.T) {
	directory := t.TempDir()
	executable, contents := filepath.Join(directory, "git"), "#!/bin/sh\nprintf '%s' \"$GIT_OPTIONAL_LOCKS\"\n"
	if runtime.GOOS == "windows" {
		executable += ".cmd"
		contents = "@echo off\r\n<nul set /p =%GIT_OPTIONAL_LOCKS%\r\n"
	}
	if err := os.WriteFile(executable, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)

	output, err := runGit(context.Background(), directory, "status")
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "0" {
		t.Fatalf("GIT_OPTIONAL_LOCKS = %q, want 0", output)
	}
}

func TestInspectorUsesMetadataOnlyGitCommands(t *testing.T) {
	responses := map[string]response{
		"symbolic-ref --quiet --short HEAD":                           {output: "feature/checkpoint\n"},
		"rev-parse --verify HEAD":                                     {output: "abc123\n"},
		"status --porcelain=v1 -z --untracked-files=all --no-renames": {output: "M  staged.go\x00 M modified.go\x00?? untracked file.txt\x00"},
		"rev-parse --abbrev-ref --symbolic-full-name @{upstream}":     {output: "origin/feature\n"},
		"rev-list --left-right --count HEAD...@{upstream}":            {output: "2\t1\n"},
		"diff --no-ext-diff HEAD --numstat --":                        {output: "3\t1\tmodified.go\n5\t0\tstaged.go\n"},
	}
	runner := &recordingRunner{responses: responses}
	inspector := &Inspector{run: runner.run}

	got, err := inspector.Inspect(context.Background(), `C:\work\repo`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "feature/checkpoint" || got.Head != "abc123" || got.Upstream == nil || got.Upstream.Ahead != 2 || got.Upstream.Behind != 1 {
		t.Fatalf("identity = %#v", got)
	}
	if !reflect.DeepEqual(got.Staged, []string{"staged.go"}) || !reflect.DeepEqual(got.Modified, []string{"modified.go"}) || !reflect.DeepEqual(got.Untracked, []string{"untracked file.txt"}) {
		t.Fatalf("status = %#v", got)
	}
	if got.Diff.FilesChanged != 2 || got.Diff.Insertions != 8 || got.Diff.Deletions != 1 {
		t.Fatalf("diff = %#v", got.Diff)
	}
	for _, args := range runner.calls {
		joined := strings.Join(args, " ")
		for _, forbidden := range []string{"show", "log", "test", "shell"} {
			if strings.Contains(joined, forbidden) {
				t.Fatalf("command %q contains forbidden operation %q", joined, forbidden)
			}
		}
	}
}

func TestInspectorDisablesConfiguredHelpers(t *testing.T) {
	repository := t.TempDir()
	runTestGit(t, repository, "init")
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repository, "add", "tracked.txt")
	runTestGit(t, repository, "-c", "user.name=CodexFolio Test", "-c", "user.email=test@example.invalid", "commit", "-m", "fixture")
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "untracked.txt"), []byte("untracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hook, contents := filepath.Join(repository, ".git", "helper"), "#!/bin/sh\n: > \"$(dirname \"$0\")/helper-executed\"\nexit 1\n"
	if runtime.GOOS == "windows" {
		hook += ".cmd"
		contents = "@echo off\r\ntype nul > \"%~dp0helper-executed\"\r\nexit /b 1\r\n"
	}
	if err := os.WriteFile(hook, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repository, "config", "core.fsmonitor", hook)
	runTestGit(t, repository, "config", "diff.external", hook)
	t.Setenv("GIT_EXTERNAL_DIFF", hook)

	inventory, err := NewInspector().Inspect(context.Background(), repository)
	if _, statErr := os.Stat(filepath.Join(repository, ".git", "helper-executed")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("configured helper executed: %v", statErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inventory.Modified, []string{"tracked.txt"}) || !reflect.DeepEqual(inventory.Untracked, []string{"untracked.txt"}) || inventory.Diff.FilesChanged != 1 {
		t.Fatalf("inventory = %#v", inventory)
	}
}

func TestInspectorKeepsMissingUpstreamExplicit(t *testing.T) {
	runner := &recordingRunner{responses: map[string]response{
		"symbolic-ref --quiet --short HEAD":                           {output: "main\n"},
		"rev-parse --verify HEAD":                                     {output: "abc123\n"},
		"status --porcelain=v1 -z --untracked-files=all --no-renames": {},
		"rev-parse --abbrev-ref --symbolic-full-name @{upstream}":     {err: errors.New("no upstream")},
		"diff --no-ext-diff HEAD --numstat --":                        {},
	}}
	got, err := (&Inspector{run: runner.run}).Inspect(context.Background(), "/repo")
	if err != nil || got.Upstream != nil {
		t.Fatalf("Inspect() = %#v, %v", got, err)
	}
}

type response struct {
	output string
	err    error
}
type recordingRunner struct {
	responses map[string]response
	calls     [][]string
}

func (runner *recordingRunner) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	runner.calls = append(runner.calls, append([]string(nil), args...))
	response := runner.responses[strings.Join(args, " ")]
	return []byte(response.output), response.err
}

func runTestGit(t *testing.T, directory string, args ...string) []byte {
	t.Helper()
	output, err := exec.Command("git", append([]string{"-C", directory}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return output
}
