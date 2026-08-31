package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/buildinfo"
	"venkatasudha.com/codex-folio/internal/platform"
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
	if !bytes.Contains(stderr.Bytes(), []byte(apperrors.StoreOpenFailed)) {
		t.Fatalf("stderr = %q, want store-open error", stderr.String())
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
