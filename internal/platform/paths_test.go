package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

func hostAbsolutePath(unix, windows string) string {
	if runtime.GOOS == "windows" {
		return windows
	}
	return unix
}

func TestResolvePathsUsesPlatformAppLocalDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		platform Platform
		env      map[string]string
		home     string
		wantRoot string
	}{
		{
			name:     "linux xdg data home",
			platform: PlatformLinux,
			env:      map[string]string{"XDG_DATA_HOME": hostAbsolutePath("/home/alice/.local/share", `C:\Users\alice\.local\share`)},
			home:     hostAbsolutePath("/home/alice", `C:\Users\alice`),
			wantRoot: filepath.Join(hostAbsolutePath("/home/alice/.local/share", `C:\Users\alice\.local\share`), linuxDirectoryName),
		},
		{
			name:     "linux home fallback",
			platform: PlatformLinux,
			home:     hostAbsolutePath("/home/alice", `C:\Users\alice`),
			wantRoot: filepath.Join(hostAbsolutePath("/home/alice/.local/share", `C:\Users\alice\.local\share`), linuxDirectoryName),
		},
		{
			name:     "windows local app data",
			platform: PlatformWindows,
			env:      map[string]string{"LOCALAPPDATA": hostAbsolutePath("/home/alice/AppData/Local", `C:\Users\alice\AppData\Local`)},
			home:     hostAbsolutePath("/home/alice", `C:\Users\alice`),
			wantRoot: filepath.Join(hostAbsolutePath("/home/alice/AppData/Local", `C:\Users\alice\AppData\Local`), appDirectoryName),
		},
		{
			name:     "macos application support",
			platform: PlatformDarwin,
			home:     hostAbsolutePath("/Users/alice", `C:\Users\alice`),
			wantRoot: filepath.Join(hostAbsolutePath("/Users/alice", `C:\Users\alice`), "Library", "Application Support", appDirectoryName),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			environment := tt.env
			if environment == nil {
				environment = map[string]string{}
			}
			paths, err := ResolvePaths(PathOptions{
				Platform:         tt.platform,
				HomeDir:          tt.home,
				OwnerHomeDir:     tt.home,
				Environment:      environment,
				WorkingDirectory: hostAbsolutePath("/work/project", `C:\work\project`),
			})
			if err != nil {
				t.Fatalf("ResolvePaths() error = %v", err)
			}
			if paths.Root != tt.wantRoot {
				t.Fatalf("Root = %q, want %q", paths.Root, tt.wantRoot)
			}
			if paths.Runtime != filepath.Join(paths.Root, "runtime") {
				t.Fatalf("Runtime = %q, want runtime below root", paths.Runtime)
			}
			if paths.MetadataFile != filepath.Join(paths.Runtime, "service.owner.json") {
				t.Fatalf("MetadataFile = %q, want service.owner.json below runtime", paths.MetadataFile)
			}
			if paths.LockFile != filepath.Join(paths.Runtime, "service.owner.lock") {
				t.Fatalf("LockFile = %q, want service.owner.lock below runtime", paths.LockFile)
			}
		})
	}
}

func TestResolvePathsAllowsNativeDefaultWhenWorkingDirectoryIsHome(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	paths, err := ResolvePaths(PathOptions{
		Platform:         PlatformLinux,
		HomeDir:          home,
		OwnerHomeDir:     home,
		Environment:      map[string]string{},
		WorkingDirectory: home,
		RepositoryRoot:   filepath.Join(t.TempDir(), "repository"),
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	if paths.Root == home || !pathWithin(paths.Root, home) {
		t.Fatalf("Root = %q, want a native app-local path below home %q", paths.Root, home)
	}
}

func TestResolvePathsUsesAbsoluteOverrideOutsideRepository(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	override := filepath.Join(t.TempDir(), "codex-folio-state")
	workingDirectory := filepath.Join(repo, "subdirectory")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}

	paths, err := ResolvePaths(PathOptions{
		Platform:          PlatformLinux,
		HomeDir:           filepath.Join(t.TempDir(), "home"),
		OwnerHomeDir:      filepath.Join(t.TempDir(), "owner-home"),
		StateRootOverride: &override,
		WorkingDirectory:  workingDirectory,
		RepositoryRoot:    repo,
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	if paths.Root != override {
		t.Fatalf("Root = %q, want %q", paths.Root, override)
	}
}

func TestResolvePathsRejectsWindowsStyleOverrideOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows-style paths are native on Windows")
	}

	override := `C:\state`
	_, err := ResolvePaths(PathOptions{
		Platform:          PlatformLinux,
		HomeDir:           t.TempDir(),
		StateRootOverride: &override,
	})
	if err == nil {
		t.Fatal("ResolvePaths() error = nil for Windows-style Unix override, want failure")
	}
	if got := apperrors.Code(err); got != apperrors.PlatformStatePathInvalid {
		t.Fatalf("error code = %q, want %q", got, apperrors.PlatformStatePathInvalid)
	}
}

func TestResolvePathsRejectsUnsafeOverrides(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	workingDirectory := filepath.Join(repo, "work")
	outside := t.TempDir()

	tests := []struct {
		name     string
		override string
		wantCode string
	}{
		{name: "empty", override: "", wantCode: apperrors.PlatformStatePathInvalid},
		{name: "relative", override: "state", wantCode: apperrors.PlatformStatePathInvalid},
		{name: "repository root", override: repo, wantCode: apperrors.PlatformStatePathUnsafe},
		{name: "repository child", override: filepath.Join(repo, "state"), wantCode: apperrors.PlatformStatePathUnsafe},
		{name: "working directory child", override: filepath.Join(workingDirectory, "state"), wantCode: apperrors.PlatformStatePathUnsafe},
		{name: "filesystem root", override: string(filepath.Separator), wantCode: apperrors.PlatformStatePathUnsafe},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			override := tt.override
			_, err := ResolvePaths(PathOptions{
				Platform:          PlatformLinux,
				HomeDir:           filepath.Join(outside, "home"),
				OwnerHomeDir:      filepath.Join(outside, "owner-home"),
				StateRootOverride: &override,
				WorkingDirectory:  workingDirectory,
				RepositoryRoot:    repo,
			})
			if err == nil {
				t.Fatal("ResolvePaths() error = nil, want failure")
			}
			if got := apperrors.Code(err); got != tt.wantCode {
				t.Fatalf("error code = %q, want %q", got, tt.wantCode)
			}
		})
	}
}
