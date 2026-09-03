package platform

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

type fixedClock struct {
	now time.Time
}

func (clock fixedClock) Now() time.Time {
	return clock.now
}

func testTempDir(t *testing.T) string {
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

func testPaths(t *testing.T) Paths {
	t.Helper()
	home := testTempDir(t)
	override := filepath.Join(testTempDir(t), "state")
	paths, err := ResolvePaths(PathOptions{
		Platform:          PlatformLinux,
		HomeDir:           home,
		OwnerHomeDir:      home,
		Environment:       map[string]string{},
		StateRootOverride: &override,
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	return paths
}

func TestOwnersUsingDifferentOverridesShareUserScope(t *testing.T) {
	home := testTempDir(t)
	firstRoot := filepath.Join(testTempDir(t), "first")
	secondRoot := filepath.Join(testTempDir(t), "second")
	first, err := ResolvePaths(PathOptions{
		Platform:          PlatformLinux,
		HomeDir:           home,
		OwnerHomeDir:      home,
		Environment:       map[string]string{"XDG_DATA_HOME": filepath.Join(home, "first-data")},
		StateRootOverride: &firstRoot,
	})
	if err != nil {
		t.Fatalf("ResolvePaths(first) error = %v", err)
	}
	second, err := ResolvePaths(PathOptions{
		Platform:          PlatformLinux,
		HomeDir:           home,
		OwnerHomeDir:      home,
		Environment:       map[string]string{"XDG_DATA_HOME": filepath.Join(home, "second-data")},
		StateRootOverride: &secondRoot,
	})
	if err != nil {
		t.Fatalf("ResolvePaths(second) error = %v", err)
	}
	if first.LockFile != second.LockFile || first.MetadataFile != second.MetadataFile {
		t.Fatalf("owner descriptors differ for one home: first=(%q,%q), second=(%q,%q)", first.LockFile, first.MetadataFile, second.LockFile, second.MetadataFile)
	}

	owner, err := Acquire(first, OwnerOptions{ProcessID: func() int { return 5001 }})
	if err != nil {
		t.Fatalf("Acquire(first) error = %v", err)
	}
	defer func() { _ = owner.Close() }()

	if _, err := Acquire(second, OwnerOptions{ProcessID: func() int { return 5002 }}); err == nil {
		t.Fatal("Acquire(second) error = nil, want user-scope contention")
	} else if got := apperrors.Code(err); got != apperrors.PlatformServiceAlreadyRunning {
		t.Fatalf("Acquire(second) error code = %q, want %q", got, apperrors.PlatformServiceAlreadyRunning)
	}
	if _, err := Discover(second, OwnerOptions{}); err == nil {
		t.Fatal("Discover(second) error = nil, want different-root rejection")
	} else if got := apperrors.Code(err); got != apperrors.PlatformServiceAlreadyRunning {
		t.Fatalf("Discover(second) error code = %q, want %q", got, apperrors.PlatformServiceAlreadyRunning)
	}
}

func TestOwnersUsingDifferentHomeDirectoriesShareUserScope(t *testing.T) {
	firstHome := testTempDir(t)
	secondHome := testTempDir(t)
	ownerHome := testTempDir(t)
	stateRoot := filepath.Join(testTempDir(t), "state")
	firstOverride := stateRoot
	secondOverride := stateRoot

	first, err := ResolvePaths(PathOptions{
		Platform:          PlatformLinux,
		HomeDir:           firstHome,
		OwnerHomeDir:      ownerHome,
		StateRootOverride: &firstOverride,
	})
	if err != nil {
		t.Fatalf("ResolvePaths(first) error = %v", err)
	}
	second, err := ResolvePaths(PathOptions{
		Platform:          PlatformLinux,
		HomeDir:           secondHome,
		OwnerHomeDir:      ownerHome,
		StateRootOverride: &secondOverride,
	})
	if err != nil {
		t.Fatalf("ResolvePaths(second) error = %v", err)
	}
	if first.LockFile != second.LockFile || first.MetadataFile != second.MetadataFile {
		t.Fatalf("owner descriptors differ for one OS user: first=(%q,%q), second=(%q,%q)", first.LockFile, first.MetadataFile, second.LockFile, second.MetadataFile)
	}

	owner, err := Acquire(first, OwnerOptions{ProcessID: func() int { return 5101 }})
	if err != nil {
		t.Fatalf("Acquire(first) error = %v", err)
	}
	defer func() { _ = owner.Close() }()

	if _, err := Acquire(second, OwnerOptions{ProcessID: func() int { return 5102 }}); err == nil {
		t.Fatal("Acquire(second) error = nil, want user-scope contention")
	} else if got := apperrors.Code(err); got != apperrors.PlatformServiceAlreadyRunning {
		t.Fatalf("Acquire(second) error code = %q, want %q", got, apperrors.PlatformServiceAlreadyRunning)
	}
}

func TestAcquireUsesFilesystemFaultSeam(t *testing.T) {
	paths := testPaths(t)
	fault := faultFileSystem{
		FileSystem:  osFileSystem{},
		openFileErr: errors.New("injected open failure"),
	}
	if _, err := Acquire(paths, OwnerOptions{FileSystem: fault}); err == nil {
		t.Fatal("Acquire() error = nil, want injected filesystem failure")
	} else if got := apperrors.Code(err); got != apperrors.PlatformPermissionDenied {
		t.Fatalf("Acquire() error code = %q, want %q", got, apperrors.PlatformPermissionDenied)
	}
}

func TestAcquireCreatesPrivateStateLayoutAndMetadata(t *testing.T) {
	paths := testPaths(t)
	wantStart := time.Date(2026, time.August, 30, 12, 34, 56, 0, time.UTC)

	owner, err := Acquire(paths, OwnerOptions{
		Clock:     fixedClock{now: wantStart},
		ProcessID: func() int { return 4242 },
	})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer func() { _ = owner.Close() }()

	for _, directory := range []string{
		paths.Root,
		paths.Runtime,
		filepath.Dir(paths.LockFile),
	} {
		info, statErr := os.Stat(directory)
		if statErr != nil {
			t.Fatalf("state directory %q: %v", directory, statErr)
		}
		if !info.IsDir() {
			t.Fatalf("state path %q is not a directory", directory)
		}
		assertPrivateMode(t, directory, 0o700)
	}

	var metadata OwnerMetadata
	encoded, err := os.ReadFile(paths.MetadataFile)
	if err != nil {
		t.Fatalf("ReadFile(metadata): %v", err)
	}
	if err := json.Unmarshal(encoded, &metadata); err != nil {
		t.Fatalf("metadata is not JSON: %v", err)
	}
	if metadata.Version != ownerMetadataVersion {
		t.Fatalf("metadata version = %d, want %d", metadata.Version, ownerMetadataVersion)
	}
	if metadata.PID != 4242 {
		t.Fatalf("metadata PID = %d, want 4242", metadata.PID)
	}
	if metadata.StateRoot != paths.Root {
		t.Fatalf("metadata state root = %q, want %q", metadata.StateRoot, paths.Root)
	}
	if !metadata.StartedAt.Equal(wantStart) {
		t.Fatalf("metadata start = %s, want %s", metadata.StartedAt, wantStart)
	}
	assertPrivateMode(t, paths.LockFile, 0o600)
	assertPrivateMode(t, paths.MetadataFile, 0o600)
}

func TestServiceClientDescriptorIsPrivateAndSeparateFromOwnerMetadata(t *testing.T) {
	paths := testPaths(t)
	owner, err := Acquire(paths, OwnerOptions{})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer func() { _ = owner.Close() }()
	const token = "private-command-token"
	want := ServiceClient{Origin: "http://127.0.0.1:4567", Token: token}
	if err := owner.PublishClient(want); err != nil {
		t.Fatalf("PublishClient() error = %v", err)
	}
	got, err := DiscoverServiceClient(paths, OwnerOptions{})
	if err != nil || got != want {
		t.Fatalf("DiscoverServiceClient() = %#v, %v; want %#v", got, err, want)
	}
	info, err := os.Stat(paths.ClientFile)
	if err != nil {
		t.Fatalf("Stat(client descriptor) error = %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("client descriptor mode = %o, want 600", info.Mode().Perm())
	}
	metadata, err := os.ReadFile(paths.MetadataFile)
	if err != nil {
		t.Fatalf("ReadFile(owner metadata) error = %v", err)
	}
	if strings.Contains(string(metadata), token) || strings.Contains(string(metadata), want.Origin) {
		t.Fatalf("owner metadata exposes private service connection: %q", metadata)
	}
}

func TestAcquireRejectsSecondWriterAndDiscoverReportsHealthyOwner(t *testing.T) {
	paths := testPaths(t)
	first, err := Acquire(paths, OwnerOptions{ProcessID: func() int { return 1001 }})
	if err != nil {
		t.Fatalf("first Acquire() error = %v", err)
	}
	defer func() { _ = first.Close() }()

	if _, err := Acquire(paths, OwnerOptions{ProcessID: func() int { return 1002 }}); err == nil {
		t.Fatal("second Acquire() error = nil, want contention")
	} else {
		if got := apperrors.Code(err); got != apperrors.PlatformServiceAlreadyRunning {
			t.Fatalf("second Acquire() error code = %q, want %q", got, apperrors.PlatformServiceAlreadyRunning)
		}
		if !errors.Is(err, ErrOwnerAlreadyRunning) {
			t.Fatalf("second Acquire() error = %v, want ErrOwnerAlreadyRunning", err)
		}
	}

	status, err := Discover(paths, OwnerOptions{})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if !status.Running {
		t.Fatal("Discover().Running = false, want true")
	}
	if status.Metadata == nil || status.Metadata.PID != 1001 {
		t.Fatalf("Discover().Metadata = %#v, want owner PID 1001", status.Metadata)
	}
}

func TestCloseReleasesOwnerAndDiscoverRemovesAbandonedMetadata(t *testing.T) {
	paths := testPaths(t)
	owner, err := Acquire(paths, OwnerOptions{ProcessID: func() int { return 2001 }})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	status, err := Discover(paths, OwnerOptions{})
	if err != nil {
		t.Fatalf("Discover() after Close error = %v", err)
	}
	if status.Running {
		t.Fatal("Discover().Running = true after Close, want false")
	}
	if _, err := os.Stat(paths.MetadataFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata after Close = %v, want absent", err)
	}

	if err := os.WriteFile(paths.MetadataFile, []byte(`{"version":1,"pid":9999,"started_at":"2020-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatalf("write abandoned metadata: %v", err)
	}
	status, err = Discover(paths, OwnerOptions{})
	if err != nil {
		t.Fatalf("Discover() with abandoned metadata error = %v", err)
	}
	if status.Running {
		t.Fatal("Discover().Running = true with free lock, want false")
	}
	if _, err := os.Stat(paths.MetadataFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abandoned metadata after discovery = %v, want absent", err)
	}

	second, err := Acquire(paths, OwnerOptions{ProcessID: func() int { return 2002 }})
	if err != nil {
		t.Fatalf("Acquire() after abandoned metadata error = %v", err)
	}
	_ = second.Close()
}

func TestAcquireDoesNotUseMetadataAgeToBreakLiveOwnership(t *testing.T) {
	paths := testPaths(t)
	owner, err := Acquire(paths, OwnerOptions{
		Clock:     fixedClock{now: time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)},
		ProcessID: func() int { return 3001 },
	})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer func() { _ = owner.Close() }()

	if _, err := Acquire(paths, OwnerOptions{
		Clock:     fixedClock{now: time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC)},
		ProcessID: func() int { return 3002 },
	}); err == nil {
		t.Fatal("Acquire() with old metadata error = nil, want live-owner contention")
	} else if got := apperrors.Code(err); got != apperrors.PlatformServiceAlreadyRunning {
		t.Fatalf("Acquire() with old metadata error code = %q, want %q", got, apperrors.PlatformServiceAlreadyRunning)
	}
}

func TestDiscoverRejectsInvalidMetadataWhileLockIsHeld(t *testing.T) {
	paths := testPaths(t)
	owner, err := Acquire(paths, OwnerOptions{ProcessID: func() int { return 4001 }})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer func() { _ = owner.Close() }()

	if err := os.WriteFile(paths.MetadataFile, []byte(`{"version":1,"pid":4001,"started_at":"2026-08-30T12:00:00Z"} trailing`), 0o600); err != nil {
		t.Fatalf("write invalid metadata: %v", err)
	}
	if _, err := Discover(paths, OwnerOptions{}); err == nil {
		t.Fatal("Discover() error = nil for invalid metadata, want failure")
	} else if got := apperrors.Code(err); got != apperrors.PlatformServiceMetadataInvalid {
		t.Fatalf("Discover() error code = %q, want %q", got, apperrors.PlatformServiceMetadataInvalid)
	}
}

func TestAcquireRejectsUnsafeOwnerLockPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}

	paths := testPaths(t)
	if err := os.MkdirAll(filepath.Dir(paths.LockFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LockFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(paths, OwnerOptions{}); err == nil {
		t.Fatal("Acquire() error = nil for unsafe lock permissions, want failure")
	} else if got := apperrors.Code(err); got != apperrors.PlatformPermissionDenied {
		t.Fatalf("Acquire() error code = %q, want %q", got, apperrors.PlatformPermissionDenied)
	}
}

func TestAcquireRejectsUnsafeExistingStateDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}

	root := testTempDir(t)
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	override := root
	paths, err := ResolvePaths(PathOptions{
		Platform:          PlatformLinux,
		HomeDir:           filepath.Join(testTempDir(t), "home"),
		OwnerHomeDir:      filepath.Join(testTempDir(t), "owner-home"),
		Environment:       map[string]string{},
		StateRootOverride: &override,
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	if _, err := Acquire(paths, OwnerOptions{}); err == nil {
		t.Fatal("Acquire() error = nil for unsafe directory, want failure")
	} else if got := apperrors.Code(err); got != apperrors.PlatformPermissionDenied {
		t.Fatalf("Acquire() error code = %q, want %q", got, apperrors.PlatformPermissionDenied)
	}
}

func TestAcquireRejectsSymlinkedStateRoot(t *testing.T) {
	target := testTempDir(t)
	link := filepath.Join(testTempDir(t), "state")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	override := link
	paths, err := ResolvePaths(PathOptions{
		Platform:          PlatformLinux,
		HomeDir:           filepath.Join(testTempDir(t), "home"),
		OwnerHomeDir:      filepath.Join(testTempDir(t), "owner-home"),
		Environment:       map[string]string{},
		StateRootOverride: &override,
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	if _, err := Acquire(paths, OwnerOptions{}); err == nil {
		t.Fatal("Acquire() error = nil for symlinked root, want failure")
	} else if got := apperrors.Code(err); got != apperrors.PlatformStatePathUnsafe {
		t.Fatalf("Acquire() error code = %q, want %q", got, apperrors.PlatformStatePathUnsafe)
	}
}

func TestAcquireRejectsSymlinkedStateAncestor(t *testing.T) {
	target := testTempDir(t)
	parent := testTempDir(t)
	link := filepath.Join(parent, "linked-parent")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(target, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(link, "state")
	paths, err := ResolvePaths(PathOptions{
		Platform:          PlatformLinux,
		HomeDir:           filepath.Join(testTempDir(t), "home"),
		OwnerHomeDir:      filepath.Join(testTempDir(t), "owner-home"),
		Environment:       map[string]string{},
		StateRootOverride: &override,
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	if _, err := Acquire(paths, OwnerOptions{}); err == nil {
		t.Fatal("Acquire() error = nil for symlinked ancestor, want failure")
	} else if got := apperrors.Code(err); got != apperrors.PlatformStatePathUnsafe {
		t.Fatalf("Acquire() error code = %q, want %q", got, apperrors.PlatformStatePathUnsafe)
	}
}

func assertPrivateMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	if got := info.Mode().Perm(); got != want.Perm() {
		t.Fatalf("mode(%q) = %04o, want %04o", path, got, want.Perm())
	}
}

type faultFileSystem struct {
	FileSystem
	openFileErr error
}

func (filesystem faultFileSystem) OpenFile(path string, flags int, permission os.FileMode) (File, error) {
	if filesystem.openFileErr != nil {
		return nil, filesystem.openFileErr
	}
	return filesystem.FileSystem.OpenFile(path, flags, permission)
}
