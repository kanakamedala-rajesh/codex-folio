package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/buildinfo"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

func TestVersionJSONIsMachineReadable(t *testing.T) {
	t.Parallel()

	want := buildinfo.Metadata{
		Product:        buildinfo.ProductName,
		Command:        buildinfo.CommandName,
		Version:        buildinfo.Version,
		SourceRevision: "abc1234",
		BuildClass:     "development",
		Dirty:          "clean",
	}
	var stdout, stderr bytes.Buffer

	if exitCode := run([]string{"version", "--json"}, &stdout, &stderr, want); exitCode != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr = %q", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var got buildinfo.Metadata
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("version output is not JSON: %v\noutput: %s", err, stdout.String())
	}
	if got != want {
		t.Fatalf("version JSON = %#v, want %#v", got, want)
	}
}

func TestVersionHumanOutputContainsBuildIdentity(t *testing.T) {
	t.Parallel()

	metadata := buildinfo.Metadata{
		Product:        buildinfo.ProductName,
		Command:        buildinfo.CommandName,
		Version:        buildinfo.Version,
		SourceRevision: "abc1234",
		BuildClass:     "development",
		Dirty:          "dirty",
	}
	var stdout, stderr bytes.Buffer

	if exitCode := run([]string{"--version"}, &stdout, &stderr, metadata); exitCode != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr = %q", exitCode, stderr.String())
	}

	for _, want := range []string{
		buildinfo.ProductName + " " + buildinfo.Version,
		"source revision: abc1234",
		"build classification: development",
		"working tree: dirty",
	} {
		if !bytes.Contains(stdout.Bytes(), []byte(want)) {
			t.Errorf("human version output %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestUnknownCommandUsesUsageExitCode(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if exitCode := run([]string{"profiles"}, &stdout, &stderr, buildinfo.Metadata{}); exitCode != exitUsage {
		t.Fatalf("run() exit code = %d, want %d", exitCode, exitUsage)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("unknown command")) {
		t.Fatalf("stderr = %q, want unknown-command diagnostic", stderr.String())
	}
	if bytes.Contains(stderr.Bytes(), []byte("profiles")) {
		t.Fatalf("stderr = %q, must not echo the command", stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte(apperrors.CLIUsage)) {
		t.Fatalf("stderr = %q, want stable error code %q", stderr.String(), apperrors.CLIUsage)
	}
}

func TestUnknownServiceCommandUsesUsageExitCodeBeforeResolvingPaths(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if exitCode := run([]string{"service", "unknown", "--state-root", "relative-state"}, &stdout, &stderr, buildinfo.Metadata{}); exitCode != exitUsage {
		t.Fatalf("run() exit code = %d, want %d", exitCode, exitUsage)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte(apperrors.CLIUsage)) {
		t.Fatalf("stderr = %q, want stable usage code %q", stderr.String(), apperrors.CLIUsage)
	}
	if bytes.Contains(stderr.Bytes(), []byte("relative-state")) {
		t.Fatalf("stderr = %q, must not echo the supplied path", stderr.String())
	}
}

func TestServiceVaultModeSelectionIsExplicitAndValidated(t *testing.T) {
	tests := []struct {
		name string
		args []string
		mode platform.VaultMode
	}{
		{name: "secret service", args: []string{"--vault-mode", "secret-service"}, mode: platform.VaultModeSecretService},
		{name: "passphrase equals", args: []string{"--vault-mode=passphrase"}, mode: platform.VaultModePassphrase},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options, err := parseServiceOptions(tt.args)
			if err != nil {
				t.Fatalf("parseServiceOptions() error = %v", err)
			}
			if options.vaultMode != tt.mode {
				t.Fatalf("vault mode = %q, want %q", options.vaultMode, tt.mode)
			}
		})
	}

	for _, args := range [][]string{
		{"--vault-mode", "plaintext"},
		{"--vault-mode", "passphrase", "--vault-mode=secret-service"},
	} {
		if _, err := parseServiceOptions(args); err == nil {
			t.Fatalf("parseServiceOptions(%q) accepted an invalid or duplicate vault mode", args)
		}
	}
}

func TestServicePassphraseInputRequiresAnExplicitNonEmptyLine(t *testing.T) {
	passphrase, err := readServiceVaultPassphrase(bytes.NewBufferString("correct horse battery staple\n"))
	if err != nil {
		t.Fatalf("readServiceVaultPassphrase() error = %v", err)
	}
	if passphrase != "correct horse battery staple" {
		t.Fatalf("passphrase = %q, want input line", passphrase)
	}

	for _, input := range []io.Reader{nil, bytes.NewBufferString("\n")} {
		if _, err := readServiceVaultPassphrase(input); err == nil {
			t.Fatalf("readServiceVaultPassphrase(%v) succeeded without an explicit passphrase", input)
		} else if got := apperrors.Code(err); got != apperrors.VaultLocked {
			t.Fatalf("readServiceVaultPassphrase(%v) error code = %q, want %q", input, got, apperrors.VaultLocked)
		}
	}
}

func TestServiceStatusJSONReportsStoppedWithoutAStateOwner(t *testing.T) {
	home := testServiceTempDir(t)
	t.Setenv("HOME", home)

	stateRoot := filepath.Join(testServiceTempDir(t), "state")
	var stdout, stderr bytes.Buffer
	if exitCode := runWithServicePathResolver([]string{"service", "status", "--state-root", stateRoot, "--json"}, &stdout, &stderr, buildinfo.Metadata{}, testServicePathResolver(home)); exitCode != exitSuccess {
		t.Fatalf("run() exit code = %d, want 0; stderr = %q", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var status struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("status output is not JSON: %v\noutput: %s", err, stdout.String())
	}
	if status.Status != "stopped" {
		t.Fatalf("status = %q, want stopped", status.Status)
	}
}

func TestServiceStartReusesExistingOwner(t *testing.T) {
	home := testServiceTempDir(t)
	t.Setenv("HOME", home)

	stateRoot := filepath.Join(testServiceTempDir(t), "state")
	override := stateRoot
	paths, err := platform.ResolvePaths(platform.PathOptions{
		Platform:          platform.Platform(runtime.GOOS),
		HomeDir:           home,
		OwnerHomeDir:      home,
		Environment:       map[string]string{},
		StateRootOverride: &override,
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	owner, err := platform.Acquire(paths, platform.OwnerOptions{ProcessID: func() int { return 7777 }})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer func() { _ = owner.Close() }()

	var stdout, stderr bytes.Buffer
	if exitCode := runWithServicePathResolver([]string{"service", "start", "--state-root", stateRoot, "--json"}, &stdout, &stderr, buildinfo.Metadata{}, testServicePathResolver(home)); exitCode != exitSuccess {
		t.Fatalf("run() exit code = %d, want 0; stderr = %q", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var status struct {
		Status string `json:"status"`
		Reused bool   `json:"reused"`
		PID    int    `json:"pid"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("start output is not JSON: %v\noutput: %s", err, stdout.String())
	}
	if status.Status != "running" || !status.Reused || status.PID != 7777 {
		t.Fatalf("status = %#v, want running/reused/PID 7777", status)
	}
}

func TestServiceOwnerLockDoesNotFollowHOME(t *testing.T) {
	firstHome := testServiceTempDir(t)
	secondHome := testServiceTempDir(t)
	stateRoot := filepath.Join(testServiceTempDir(t), "state")

	t.Setenv("HOME", firstHome)
	first, err := resolveCLIPaths(&stateRoot)
	if err != nil {
		t.Fatalf("resolveCLIPaths(first) error = %v", err)
	}
	t.Setenv("HOME", secondHome)
	second, err := resolveCLIPaths(&stateRoot)
	if err != nil {
		t.Fatalf("resolveCLIPaths(second) error = %v", err)
	}
	if first.LockFile != second.LockFile || first.MetadataFile != second.MetadataFile {
		t.Fatalf("owner descriptors follow HOME: first=(%q,%q), second=(%q,%q)", first.LockFile, first.MetadataFile, second.LockFile, second.MetadataFile)
	}
}

func TestServiceRejectsRelativeStateRootWithStableSafeError(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if exitCode := run([]string{"service", "status", "--state-root", "relative-state"}, &stdout, &stderr, buildinfo.Metadata{}); exitCode != exitUsage {
		t.Fatalf("run() exit code = %d, want %d", exitCode, exitUsage)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte(apperrors.PlatformStatePathInvalid)) {
		t.Fatalf("stderr = %q, want stable path error %q", stderr.String(), apperrors.PlatformStatePathInvalid)
	}
	if bytes.Contains(stderr.Bytes(), []byte("relative-state")) {
		t.Fatalf("stderr = %q, must not echo the supplied path", stderr.String())
	}
}

func TestServiceDiagnosticRedactsSensitiveVaultCause(t *testing.T) {
	t.Parallel()

	const (
		plaintextSentinel  = "diagnostic plaintext sentinel"
		keySentinel        = "diagnostic key bytes sentinel"
		ciphertextSentinel = "diagnostic ciphertext sentinel"
		platformSentinel   = "diagnostic platform detail sentinel"
	)
	resolver := func(*string) (platform.Paths, error) {
		return platform.Paths{}, apperrors.New(apperrors.VaultLocked, errors.New(
			plaintextSentinel+" "+keySentinel+" "+ciphertextSentinel+" "+platformSentinel,
		))
	}
	var stdout, stderr bytes.Buffer

	if exitCode := runWithServicePathResolver(
		[]string{"service", "status", "--state-root", filepath.Join(t.TempDir(), "state")},
		&stdout, &stderr, buildinfo.Metadata{}, resolver,
	); exitCode != exitFailure {
		t.Fatalf("run() exit code = %d, want %d; stdout = %q; stderr = %q", exitCode, exitFailure, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	for _, forbidden := range []string{plaintextSentinel, keySentinel, ciphertextSentinel, platformSentinel} {
		if bytes.Contains(stderr.Bytes(), []byte(forbidden)) {
			t.Fatalf("stderr = %q, must not contain sensitive sentinel %q", stderr.String(), forbidden)
		}
	}
	if !bytes.Contains(stderr.Bytes(), []byte(apperrors.VaultLocked)) {
		t.Fatalf("stderr = %q, want stable vault error %q", stderr.String(), apperrors.VaultLocked)
	}
	if !bytes.Contains(stderr.Bytes(), []byte(serviceRemediation(apperrors.VaultLocked))) {
		t.Fatalf("stderr = %q, want safe remediation", stderr.String())
	}
}

func TestServiceDiagnosticSinkStoresOnlyStableRedactedProjection(t *testing.T) {
	t.Parallel()

	const sentinel = "login identity repository path cookie bootstrap session csrf envelope passphrase ciphertext raw provider payload"
	resolver := func(*string) (platform.Paths, error) {
		return platform.Paths{}, apperrors.New(apperrors.VaultLocked, errors.New(sentinel))
	}
	var events []diagnostics.Event
	sink := diagnostics.SinkFunc(func(event diagnostics.Event) error {
		events = append(events, event)
		return nil
	})
	var stdout, stderr bytes.Buffer

	if exitCode := runServiceWithInputAndDiagnostics(
		[]string{"status", "--state-root", filepath.Join(t.TempDir(), "sensitive-state")},
		nil,
		&stdout,
		&stderr,
		resolver,
		sink,
	); exitCode != exitFailure {
		t.Fatalf("runServiceWithInputAndDiagnostics() exit code = %d, want %d; stderr = %q", exitCode, exitFailure, stderr.String())
	}
	if len(events) != 1 {
		t.Fatalf("diagnostic events = %#v, want one event", events)
	}
	if events[0].ErrorCode != apperrors.VaultLocked || events[0].Component != diagnostics.ComponentVault {
		t.Fatalf("diagnostic event = %#v, want stable vault projection", events[0])
	}
	if events[0].Context == nil || events[0].Context.Operation != diagnostics.OperationCommand || events[0].Context.State != diagnostics.StateLocked {
		t.Fatalf("diagnostic context = %#v, want bounded command/locked context", events[0].Context)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("json.Marshal(events) error = %v", err)
	}
	if bytes.Contains(encoded, []byte(sentinel)) {
		t.Fatalf("diagnostic events contain a forbidden cause: %s", encoded)
	}
}

func TestServiceStateDoesNotDependOnWorkingRepository(t *testing.T) {
	home := testServiceTempDir(t)
	t.Setenv("HOME", home)

	stateRoot := filepath.Join(testServiceTempDir(t), "state")
	var stdout, stderr bytes.Buffer
	if exitCode := runWithServicePathResolver([]string{"service", "status", "--state-root", stateRoot}, &stdout, &stderr, buildinfo.Metadata{}, testServicePathResolver(home)); exitCode != exitSuccess {
		t.Fatalf("run() exit code = %d, want 0; stderr = %q", exitCode, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(stateRoot, "runtime")); err != nil {
		t.Fatalf("state was not created outside the repository: %v", err)
	}
}

func TestServicePathsResolveForAbsoluteTemporaryOverride(t *testing.T) {
	home := testServiceTempDir(t)
	t.Setenv("HOME", home)
	stateRoot := filepath.Join(testServiceTempDir(t), "state")

	paths, err := resolveCLIPaths(&stateRoot)
	if err != nil {
		t.Fatalf("resolveCLIPaths() error = %v", err)
	}
	if paths.Root != stateRoot {
		t.Fatalf("Root = %q, want %q", paths.Root, stateRoot)
	}
}

func TestServiceRecoveryListsAndRestoresOnlyAnExplicitRedactedCandidate(t *testing.T) {
	home := testServiceTempDir(t)
	stateRoot := filepath.Join(testServiceTempDir(t), "state")
	override := stateRoot
	paths, err := platform.ResolvePaths(platform.PathOptions{
		Platform:          platform.Platform(runtime.GOOS),
		HomeDir:           home,
		OwnerHomeDir:      home,
		Environment:       map[string]string{},
		StateRootOverride: &override,
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		t.Fatalf("MkdirAll() state root error = %v", err)
	}
	foundation, err := store.Open(paths.DatabaseFile)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	if _, err := foundation.CreateRecoveryCheckpoint(nil); err != nil {
		t.Fatalf("CreateRecoveryCheckpoint() error = %v", err)
	}
	if err := foundation.Close(); err != nil {
		t.Fatalf("store.Close() error = %v", err)
	}

	resolver := testServicePathResolver(home)
	var listStdout, listStderr bytes.Buffer
	if exitCode := runWithServicePathResolver([]string{"service", "recovery", "list", "--state-root", stateRoot, "--json"}, &listStdout, &listStderr, buildinfo.Metadata{}, resolver); exitCode != exitSuccess {
		t.Fatalf("recovery list exit code = %d, want 0; stderr = %q", exitCode, listStderr.String())
	}
	if listStderr.Len() != 0 {
		t.Fatalf("recovery list stderr = %q, want empty", listStderr.String())
	}
	var listed struct {
		Candidates []store.RecoveryCandidate `json:"candidates"`
	}
	if err := json.Unmarshal(listStdout.Bytes(), &listed); err != nil {
		t.Fatalf("recovery list output is not JSON: %v\noutput: %s", err, listStdout.String())
	}
	if len(listed.Candidates) == 0 || !listed.Candidates[0].Valid {
		t.Fatalf("recovery candidates = %#v, want a valid candidate", listed.Candidates)
	}
	for _, forbidden := range []string{paths.DatabaseFile, filepath.Join(paths.Root, "recovery")} {
		if bytes.Contains(listStdout.Bytes(), []byte(forbidden)) {
			t.Fatalf("recovery list output contains forbidden path %q: %q", forbidden, listStdout.String())
		}
	}

	var restoreStdout, restoreStderr bytes.Buffer
	if exitCode := runWithServicePathResolver([]string{"service", "recovery", "restore", "--state-root", stateRoot, "--candidate", listed.Candidates[0].ID, "--json"}, &restoreStdout, &restoreStderr, buildinfo.Metadata{}, resolver); exitCode != exitSuccess {
		t.Fatalf("recovery restore exit code = %d, want 0; stderr = %q", exitCode, restoreStderr.String())
	}
	if restoreStderr.Len() != 0 {
		t.Fatalf("recovery restore stderr = %q, want empty", restoreStderr.String())
	}
	var restored store.RecoveryResult
	if err := json.Unmarshal(restoreStdout.Bytes(), &restored); err != nil {
		t.Fatalf("recovery restore output is not JSON: %v\noutput: %s", err, restoreStdout.String())
	}
	if restored.CandidateID != listed.Candidates[0].ID || restored.PreservedDatabaseID == "" {
		t.Fatalf("recovery restore result = %#v, want selected candidate and preserved state", restored)
	}
	if bytes.Contains(restoreStdout.Bytes(), []byte(paths.DatabaseFile)) || bytes.Contains(restoreStdout.Bytes(), []byte(paths.Root)) {
		t.Fatalf("recovery restore output contains a canonical path: %q", restoreStdout.String())
	}
}

func TestServiceRecoveryRestoreRequiresAnExplicitCandidate(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exitCode := runWithServicePathResolver([]string{"service", "recovery", "restore", "--state-root", filepath.Join(t.TempDir(), "state")}, &stdout, &stderr, buildinfo.Metadata{}, func(*string) (platform.Paths, error) {
		t.Fatal("path resolver should not run before candidate validation")
		return platform.Paths{}, nil
	}); exitCode != exitUsage {
		t.Fatalf("recovery restore exit code = %d, want %d; stderr = %q", exitCode, exitUsage, stderr.String())
	}
	if stdout.Len() != 0 || !bytes.Contains(stderr.Bytes(), []byte("requires --candidate")) {
		t.Fatalf("recovery restore output = %q / %q, want usage diagnostic", stdout.String(), stderr.String())
	}
}

func TestServiceRecoveryRejectsACompetingLiveServiceOwner(t *testing.T) {
	home := testServiceTempDir(t)
	stateRoot := filepath.Join(testServiceTempDir(t), "state")
	override := stateRoot
	paths, err := platform.ResolvePaths(platform.PathOptions{
		Platform:          platform.Platform(runtime.GOOS),
		HomeDir:           home,
		OwnerHomeDir:      home,
		Environment:       map[string]string{},
		StateRootOverride: &override,
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	owner, err := platform.Acquire(paths, platform.OwnerOptions{ProcessID: func() int { return 9901 }})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer func() { _ = owner.Close() }()

	var stdout, stderr bytes.Buffer
	if exitCode := runWithServicePathResolver([]string{"service", "recovery", "list", "--state-root", stateRoot, "--json"}, &stdout, &stderr, buildinfo.Metadata{}, testServicePathResolver(home)); exitCode != exitFailure {
		t.Fatalf("recovery list exit code = %d, want %d; stderr = %q", exitCode, exitFailure, stderr.String())
	}
	if stdout.Len() != 0 || !bytes.Contains(stderr.Bytes(), []byte(apperrors.PlatformServiceAlreadyRunning)) {
		t.Fatalf("recovery list output = %q / %q, want live-owner error", stdout.String(), stderr.String())
	}
}

func TestServiceStartFailsClosedWhenStoreCannotOpen(t *testing.T) {
	home := testServiceTempDir(t)
	stateRoot := filepath.Join(testServiceTempDir(t), "state")
	override := stateRoot
	paths, err := platform.ResolvePaths(platform.PathOptions{
		Platform:          platform.Platform(runtime.GOOS),
		HomeDir:           home,
		OwnerHomeDir:      home,
		Environment:       map[string]string{},
		StateRootOverride: &override,
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		t.Fatalf("MkdirAll(state root): %v", err)
	}
	if err := os.Mkdir(paths.DatabaseFile, 0o700); err != nil {
		t.Fatalf("Mkdir(database path): %v", err)
	}

	var stdout, stderr bytes.Buffer
	if exitCode := runServiceStart(paths, serviceOptions{json: true}, &stdout, &stderr); exitCode != exitFailure {
		t.Fatalf("runServiceStart() exit code = %d, want %d; stdout = %q; stderr = %q", exitCode, exitFailure, stdout.String(), stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte(apperrors.StoreOpenFailed)) && !bytes.Contains(stderr.Bytes(), []byte(apperrors.VaultUnavailable)) {
		t.Fatalf("stderr = %q, want store-open or secure-vault error", stderr.String())
	}
	status, err := platform.Discover(paths, platform.OwnerOptions{})
	if err != nil {
		t.Fatalf("Discover() after failed start: %v", err)
	}
	if status.Running {
		t.Fatal("state owner remains running after failed store startup")
	}
}

func testServicePathResolver(home string) servicePathResolver {
	return func(override *string) (platform.Paths, error) {
		workingDirectory, err := os.Getwd()
		if err != nil {
			return platform.Paths{}, err
		}
		return platform.ResolvePaths(platform.PathOptions{
			Platform:          platform.Platform(runtime.GOOS),
			HomeDir:           home,
			OwnerHomeDir:      home,
			StateRootOverride: override,
			WorkingDirectory:  workingDirectory,
			RepositoryRoot:    filepath.Join(filepath.Dir(home), "codex-folio-test-repository"),
		})
	}
}

func testServiceTempDir(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		return t.TempDir()
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", home, err)
	}
	directory, err := os.MkdirTemp(home, "codex-folio-test-")
	if err != nil {
		t.Fatalf("MkdirTemp(%q): %v", home, err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("RemoveAll(%q): %v", directory, err)
		}
	})
	return directory
}
