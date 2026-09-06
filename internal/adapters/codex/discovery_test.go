package codex

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

func TestResolverFindsAndValidatesOnePATHExecutable(t *testing.T) {
	directory := t.TempDir()
	executable := writeFakeCodex(t, directory, "codex-cli 0.1.2", "")
	resolver := NewResolver(ResolverOptions{PathEnvironment: directory})

	candidate, err := resolver.Resolve("")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if candidate.Path != executable || candidate.Version != "0.1.2" {
		t.Fatalf("candidate = %#v, want path %q and version 0.1.2", candidate, executable)
	}
}

func TestResolverExplicitAbsoluteOverrideWinsOverPATH(t *testing.T) {
	pathDirectory := t.TempDir()
	pathExecutable := writeFakeCodex(t, pathDirectory, "codex-cli 0.1.2", "")
	overrideDirectory := t.TempDir()
	overrideExecutable := writeFakeCodex(t, overrideDirectory, "codex-cli 0.2.0", "")
	resolver := NewResolver(ResolverOptions{PathEnvironment: pathDirectory})

	candidate, err := resolver.Resolve(overrideExecutable)
	if err != nil {
		t.Fatalf("Resolve(override) error = %v", err)
	}
	if candidate.Path != overrideExecutable || candidate.Version != "0.2.0" {
		t.Fatalf("candidate = %#v, want override %q/0.2.0; PATH candidate was %q", candidate, overrideExecutable, pathExecutable)
	}
}

func TestResolverRejectsRelativeMissingAndNonExecutableOverrides(t *testing.T) {
	nonExecutable := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(nonExecutable, []byte("not executable"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	resolver := NewResolver(ResolverOptions{PathEnvironment: ""})
	for _, candidate := range []string{
		"codex",
		filepath.Join(t.TempDir(), "missing-codex"),
		nonExecutable,
	} {
		if _, err := resolver.Resolve(candidate); err == nil || apperrors.Code(err) != apperrors.LaunchCodexPathInvalid {
			t.Fatalf("Resolve(%q) error = %v, want %q", candidate, err, apperrors.LaunchCodexPathInvalid)
		}
	}
}

func TestResolverRejectsAmbiguousPATHAndInvalidVersion(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	writeFakeCodex(t, first, "codex-cli 0.1.2", "")
	writeFakeCodex(t, second, "codex-cli 0.1.3", "")
	resolver := NewResolver(ResolverOptions{PathEnvironment: strings.Join([]string{first, second}, string(os.PathListSeparator))})

	if _, err := resolver.Resolve(""); err == nil || apperrors.Code(err) != apperrors.LaunchCodexAmbiguous {
		t.Fatalf("ambiguous Resolve() error = %v, want %q", err, apperrors.LaunchCodexAmbiguous)
	}

	invalidDirectory := t.TempDir()
	writeFakeCodex(t, invalidDirectory, "not-a-version", "")
	resolver = NewResolver(ResolverOptions{PathEnvironment: invalidDirectory})
	if _, err := resolver.Resolve(""); err == nil || apperrors.Code(err) != apperrors.LaunchCodexVersionInvalid {
		t.Fatalf("invalid-version Resolve() error = %v, want %q", err, apperrors.LaunchCodexVersionInvalid)
	}
}

func TestResolverUsesOnlyNonMutatingVersionInvocation(t *testing.T) {
	directory := t.TempDir()
	logPath := filepath.Join(directory, "arguments.log")
	writeFakeCodex(t, directory, "codex-cli 0.1.2", logPath)
	resolver := NewResolver(ResolverOptions{PathEnvironment: directory})

	if _, err := resolver.Resolve(""); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	arguments, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(arguments.log) error = %v", err)
	}
	if strings.TrimSpace(string(arguments)) != "--version" {
		t.Fatalf("Codex invocation arguments = %q, want --version only", arguments)
	}
}

func TestResolverDeduplicatesSymlinkedPATHCandidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows symlink creation requires host-specific privileges")
	}
	first := t.TempDir()
	executable := writeFakeCodex(t, first, "codex-cli 0.1.2", "")
	second := t.TempDir()
	if err := os.Symlink(executable, filepath.Join(second, "codex")); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(ResolverOptions{PathEnvironment: strings.Join([]string{first, second}, string(os.PathListSeparator))})
	if _, err := resolver.Resolve(""); err != nil {
		t.Fatalf("Resolve() error = %v, want one canonical candidate", err)
	}
}

func TestResolverDeduplicatesWindowsPATHCase(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path comparison")
	}
	directory := t.TempDir()
	writeFakeCodex(t, directory, "codex-cli 0.1.2", "")
	resolver := NewResolver(ResolverOptions{PathEnvironment: strings.Join([]string{directory, strings.ToUpper(directory)}, string(os.PathListSeparator))})
	if _, err := resolver.Resolve(""); err != nil {
		t.Fatalf("Resolve() error = %v, want case-insensitive deduplication", err)
	}
}

func TestResolverDoesNotExposeCandidateDetailsInErrors(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "private-codex-candidate")
	resolver := NewResolver(ResolverOptions{PathEnvironment: ""})
	_, err := resolver.Resolve(sentinel)
	if err == nil {
		t.Fatal("Resolve() error = nil, want missing-candidate failure")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Resolve() should expose only the stable coded error, got %v", err)
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("Resolve() error = %q, must not expose candidate path", err)
	}
}

func writeFakeCodex(t *testing.T, directory, version, logPath string) string {
	t.Helper()
	name := "codex"
	quotedVersion := shellQuote(version)
	logCommand := ""
	if logPath != "" {
		logCommand = "printf '%s\\n' \"$*\" > " + shellQuote(logPath) + "\n"
	}
	script := "#!/bin/sh\n" + logCommand + "printf '%s\\n' " + quotedVersion + "\n"
	if runtime.GOOS == "windows" {
		name = "codex.cmd"
		quotedLogPath := strings.ReplaceAll(logPath, `"`, `""`)
		script = "@echo off\r\n"
		if logPath != "" {
			script += "echo %* > \"" + quotedLogPath + "\"\r\n"
		}
		script += "echo " + version + "\r\n"
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
