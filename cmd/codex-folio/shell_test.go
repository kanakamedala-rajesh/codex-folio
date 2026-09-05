package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/platform"
)

func TestShellGenerateBashCompletionIsInspectableAndManual(t *testing.T) {
	root := shellTestTempDir(t)
	paths := platform.Paths{Root: root}
	var stdout, stderr bytes.Buffer

	if code := runShell(
		[]string{"generate", "--shell", "bash", "--json"},
		&stdout,
		&stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		"0.0.1-alpha",
	); code != exitSuccess {
		t.Fatalf("runShell() exit code = %d, want %d; stderr = %q", code, exitSuccess, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var result shellIntegrationResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("shell output is not JSON: %v; output = %q", err, stdout.String())
	}
	if result.Action != "generated" || result.Shell != "bash" || result.Kind != "completion" || result.Version != "0.0.1-alpha" {
		t.Fatalf("result = %#v, want generated bash completion metadata", result)
	}
	content, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", result.Path, err)
	}
	for _, want := range []string{
		"codex-folio-shell-integration",
		"complete -F",
		"codex-folio",
		"launch",
		"select",
	} {
		if !strings.Contains(string(content), want) {
			t.Errorf("generated script does not contain %q: %q", want, content)
		}
	}
	encodedSourceLine, err := json.Marshal(result.SourceLine)
	if err != nil {
		t.Fatalf("source instruction JSON error = %v", err)
	}
	if !bytes.Contains(stdout.Bytes(), encodedSourceLine) {
		t.Fatalf("JSON output does not include source instruction %q: %q", result.SourceLine, stdout.String())
	}
	if result.SourceLine != "source "+shellQuoteForTest(result.Path) {
		t.Fatalf("source line = %q, want source instruction for generated path", result.SourceLine)
	}
	if result.RemovalLine == "" || strings.Contains(result.RemovalLine, "rm ") {
		t.Fatalf("removal line = %q, want CodexFolio removal command", result.RemovalLine)
	}
}

func shellQuoteForTest(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}

func TestShellGenerateUsesAppLocalBoundaryWithoutMutatingShellOrCodex(t *testing.T) {
	root := filepath.Join(shellTestTempDir(t), "state")
	paths := platform.Paths{Root: root}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll(state): %v", err)
	}
	startup := filepath.Join(t.TempDir(), ".bashrc")
	startupContent := []byte("# user startup\n")
	if err := os.WriteFile(startup, startupContent, 0o600); err != nil {
		t.Fatalf("WriteFile(startup): %v", err)
	}
	codexBinary := filepath.Join(root, "codex")
	codexContent := []byte("user-installed Codex placeholder")
	if err := os.WriteFile(codexBinary, codexContent, 0o600); err != nil {
		t.Fatalf("WriteFile(codex): %v", err)
	}
	var stdout, stderr bytes.Buffer

	if code := runShell(
		[]string{"generate", "--shell", "bash"},
		&stdout,
		&stderr,
		func(*string) (platform.Paths, error) { return paths, nil },
		"0.0.1-alpha",
	); code != exitSuccess {
		t.Fatalf("runShell() exit code = %d, want %d; stderr = %q", code, exitSuccess, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "shell-integration")); err != nil {
		t.Fatalf("shell integration directory was not created: %v", err)
	}
	if got, err := os.ReadFile(startup); err != nil || !bytes.Equal(got, startupContent) {
		t.Fatalf("startup content/error = %q/%v, want unchanged startup file", got, err)
	}
	if got, err := os.ReadFile(codexBinary); err != nil || !bytes.Equal(got, codexContent) {
		t.Fatalf("Codex content/error = %q/%v, want unchanged installed binary", got, err)
	}
}

func TestShellGeneratesPowerShellCompletionAndDistinctWrapper(t *testing.T) {
	root := shellTestTempDir(t)
	paths := platform.Paths{Root: root}

	var completionOut, completionErr bytes.Buffer
	if code := runShell(
		[]string{"generate", "--shell=powershell", "--json"},
		&completionOut,
		&completionErr,
		func(*string) (platform.Paths, error) { return paths, nil },
		"0.0.1-alpha",
	); code != exitSuccess || completionErr.Len() != 0 {
		t.Fatalf("PowerShell completion exit/stderr = %d/%q, want success", code, completionErr.String())
	}
	var completion shellIntegrationResult
	if err := json.Unmarshal(completionOut.Bytes(), &completion); err != nil {
		t.Fatalf("PowerShell completion JSON error = %v", err)
	}
	content, err := os.ReadFile(completion.Path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", completion.Path, err)
	}
	for _, want := range []string{"Register-ArgumentCompleter", "'launch'", "'select'", "codex-folio"} {
		if !strings.Contains(string(content), want) {
			t.Errorf("PowerShell completion does not contain %q: %q", want, content)
		}
	}
	if completion.SourceLine != ". "+shellQuoteForPowerShellTest(completion.Path) {
		t.Fatalf("PowerShell source line = %q, want dot-source instruction", completion.SourceLine)
	}

	var wrapperOut, wrapperErr bytes.Buffer
	if code := runShell(
		[]string{"generate", "--shell", "powershell", "--wrapper", "--json"},
		&wrapperOut,
		&wrapperErr,
		func(*string) (platform.Paths, error) { return paths, nil },
		"0.0.1-alpha",
	); code != exitSuccess || wrapperErr.Len() != 0 {
		t.Fatalf("PowerShell wrapper exit/stderr = %d/%q, want success", code, wrapperErr.String())
	}
	var wrapper shellIntegrationResult
	if err := json.Unmarshal(wrapperOut.Bytes(), &wrapper); err != nil {
		t.Fatalf("PowerShell wrapper JSON error = %v", err)
	}
	wrapperContent, err := os.ReadFile(wrapper.Path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", wrapper.Path, err)
	}
	if !strings.Contains(string(wrapperContent), "Invoke-CodexFolioLaunch") || !strings.Contains(string(wrapperContent), "codex-folio launch") || strings.Contains(string(wrapperContent), "function codex") {
		t.Fatalf("wrapper content = %q, want distinct foreground launcher", wrapperContent)
	}
}

func TestShellGenerationAndRemovalAreIdempotent(t *testing.T) {
	root := shellTestTempDir(t)
	paths := platform.Paths{Root: root}
	resolver := func(*string) (platform.Paths, error) { return paths, nil }

	var firstOut, firstErr bytes.Buffer
	if code := runShell([]string{"generate", "--shell", "zsh", "--json"}, &firstOut, &firstErr, resolver, "0.0.1-alpha"); code != exitSuccess {
		t.Fatalf("first generate exit code = %d; stderr = %q", code, firstErr.String())
	}
	var first shellIntegrationResult
	if err := json.Unmarshal(firstOut.Bytes(), &first); err != nil {
		t.Fatalf("first generate JSON error = %v", err)
	}
	before, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatalf("ReadFile(first): %v", err)
	}

	var secondOut, secondErr bytes.Buffer
	if code := runShell([]string{"generate", "--shell", "zsh", "--json"}, &secondOut, &secondErr, resolver, "0.0.1-alpha"); code != exitSuccess || secondErr.Len() != 0 {
		t.Fatalf("second generate exit/stderr = %d/%q", code, secondErr.String())
	}
	after, err := os.ReadFile(first.Path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("regenerated content/error = %q/%v, want identical content", after, err)
	}

	var removedOut, removedErr bytes.Buffer
	if code := runShell([]string{"remove", "--shell", "zsh", "--json"}, &removedOut, &removedErr, resolver, "0.0.1-alpha"); code != exitSuccess || removedErr.Len() != 0 {
		t.Fatalf("remove exit/stderr = %d/%q", code, removedErr.String())
	}
	var removed shellIntegrationResult
	if err := json.Unmarshal(removedOut.Bytes(), &removed); err != nil {
		t.Fatalf("remove JSON error = %v", err)
	}
	if removed.Action != "removed" || !removed.Present {
		t.Fatalf("remove result = %#v, want removed/present", removed)
	}
	if _, err := os.Stat(first.Path); !os.IsNotExist(err) {
		t.Fatalf("removed integration stat error = %v, want not found", err)
	}

	var absentOut, absentErr bytes.Buffer
	if code := runShell([]string{"remove", "--shell", "zsh", "--json"}, &absentOut, &absentErr, resolver, "0.0.1-alpha"); code != exitSuccess || absentErr.Len() != 0 {
		t.Fatalf("idempotent remove exit/stderr = %d/%q", code, absentErr.String())
	}
	var absent shellIntegrationResult
	if err := json.Unmarshal(absentOut.Bytes(), &absent); err != nil {
		t.Fatalf("absent JSON error = %v", err)
	}
	if absent.Action != "absent" || absent.Present {
		t.Fatalf("absent result = %#v, want absent/not present", absent)
	}
}

func TestShellRefusesChangedOrInaccessibleIntegrationWithoutDeletingIt(t *testing.T) {
	root := shellTestTempDir(t)
	paths := platform.Paths{Root: root}
	resolver := func(*string) (platform.Paths, error) { return paths, nil }

	var generated bytes.Buffer
	if code := runShell([]string{"generate", "--shell", "bash", "--json"}, &generated, io.Discard, resolver, "0.0.1-alpha"); code != exitSuccess {
		t.Fatalf("generate exit code = %d", code)
	}
	var result shellIntegrationResult
	if err := json.Unmarshal(generated.Bytes(), &result); err != nil {
		t.Fatalf("generate JSON error = %v", err)
	}
	const changed = "# user-authored integration\n"
	if err := os.WriteFile(result.Path, []byte(changed), 0o600); err != nil {
		t.Fatalf("WriteFile(changed): %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runShell([]string{"remove", "--shell", "bash"}, &stdout, &stderr, resolver, "0.0.1-alpha"); code != exitFailure {
		t.Fatalf("changed remove exit code = %d, want failure; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "CF_CLI_SHELL_INTEGRATION_INVALID") {
		t.Fatalf("changed remove stderr = %q, want stable invalid code", stderr.String())
	}
	content, err := os.ReadFile(result.Path)
	if err != nil || string(content) != changed {
		t.Fatalf("changed integration content/error = %q/%v, want preserved content", content, err)
	}

	versioned := renderShellIntegration("bash", shellKindCompletion, "9.9.9")
	if err := os.WriteFile(result.Path, []byte(versioned), 0o600); err != nil {
		t.Fatalf("WriteFile(versioned): %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runShell([]string{"remove", "--shell", "bash"}, &stdout, &stderr, resolver, "0.0.1-alpha"); code != exitFailure {
		t.Fatalf("incompatible-version remove exit code = %d, want failure; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "CF_CLI_SHELL_INTEGRATION_INVALID") {
		t.Fatalf("incompatible-version stderr = %q, want stable invalid code", stderr.String())
	}
	if got, err := os.ReadFile(result.Path); err != nil || string(got) != versioned {
		t.Fatalf("incompatible-version content/error = %q/%v, want preserved content", got, err)
	}

	canonical := renderShellIntegration("bash", shellKindCompletion, "0.0.1-alpha")
	headerEnd := strings.IndexByte(canonical, '\n')
	modifiedBody := canonical[headerEnd+1:] + "# user-authored change\n"
	digest := sha256.Sum256([]byte(modifiedBody))
	tampered := fmt.Sprintf("%s digest=sha256:%s\n%s", canonical[:strings.Index(canonical, " digest=")], hex.EncodeToString(digest[:]), modifiedBody)
	if err := os.WriteFile(result.Path, []byte(tampered), 0o600); err != nil {
		t.Fatalf("WriteFile(tampered): %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runShell([]string{"remove", "--shell", "bash"}, &stdout, &stderr, resolver, "0.0.1-alpha"); code != exitFailure {
		t.Fatalf("tampered remove exit code = %d, want failure; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "CF_CLI_SHELL_INTEGRATION_INVALID") {
		t.Fatalf("tampered stderr = %q, want stable invalid code", stderr.String())
	}
	if got, err := os.ReadFile(result.Path); err != nil || string(got) != tampered {
		t.Fatalf("tampered content/error = %q/%v, want preserved content", got, err)
	}

	headerMutation := strings.Replace(canonical, " digest=", " note=user digest=", 1)
	if err := os.WriteFile(result.Path, []byte(headerMutation), 0o600); err != nil {
		t.Fatalf("WriteFile(header mutation): %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runShell([]string{"remove", "--shell", "bash"}, &stdout, &stderr, resolver, "0.0.1-alpha"); code != exitFailure {
		t.Fatalf("header mutation remove exit code = %d, want failure; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "CF_CLI_SHELL_INTEGRATION_INVALID") {
		t.Fatalf("header mutation stderr = %q, want stable invalid code", stderr.String())
	}
	if got, err := os.ReadFile(result.Path); err != nil || string(got) != headerMutation {
		t.Fatalf("header mutation content/error = %q/%v, want preserved content", got, err)
	}

	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile(blocked): %v", err)
	}
	blockedPaths := platform.Paths{Root: root, ShellIntegration: filepath.Join(blocked, "shell-integration")}
	var blockedOut, blockedErr bytes.Buffer
	if code := runShell([]string{"generate", "--shell", "bash"}, &blockedOut, &blockedErr, func(*string) (platform.Paths, error) { return blockedPaths, nil }, "0.0.1-alpha"); code != exitFailure {
		t.Fatalf("inaccessible generate exit code = %d, want failure; stderr = %q", code, blockedErr.String())
	}
	if !strings.Contains(blockedErr.String(), "CF_CLI_SHELL_INTEGRATION_FAILED") {
		t.Fatalf("inaccessible stderr = %q, want stable failed code", blockedErr.String())
	}
}

func shellQuoteForPowerShellTest(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "''") + "'"
}

func shellTestTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(temp dir): %v", err)
	}
	return path
}
