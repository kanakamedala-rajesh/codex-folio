// Package platform contains adapters for operating-system facilities used by
// the local service boundary.
package platform

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

const (
	PlatformLinux   Platform = "linux"
	PlatformWindows Platform = "windows"
	PlatformDarwin  Platform = "darwin"
)

const (
	appDirectoryName   = "CodexFolio"
	linuxDirectoryName = "codex-folio"
	runtimeDirectory   = "runtime"
	lockFileName       = "service.owner.lock"
	metadataFileName   = "service.owner.json"
	vaultFileName      = "codex-folio.vault"
	managedHomesName   = "managed-homes"
)

// Platform identifies the operating-system path convention to use. It is
// explicit in PathOptions so path behavior can be tested without depending on
// the host running the test.
type Platform string

// PathOptions supplies platform and environment inputs for path resolution.
// A non-nil StateRootOverride represents an explicitly supplied command-line
// value, including an explicit empty value, which is rejected.
type PathOptions struct {
	Platform          Platform
	HomeDir           string // app-local default root; may come from the environment.
	OwnerHomeDir      string // canonical owner runtime home; empty loads from the OS user database.
	Environment       map[string]string
	FileSystem        FileSystem
	StateRootOverride *string
	WorkingDirectory  string
	RepositoryRoot    string
}

// Paths is the selected app-local state layout. Runtime is below Root; the
// owner lock and metadata use a canonical user-scoped runtime so alternate
// path overrides cannot create a second owner.
type Paths struct {
	Root         string
	Runtime      string
	LockFile     string
	MetadataFile string
	DatabaseFile string
	VaultFile    string
	ManagedHomes string
}

// ResolvePaths resolves the platform default or an explicit absolute state
// root without consulting the current working directory for default state.
func ResolvePaths(options PathOptions) (Paths, error) {
	platform := options.Platform
	if platform == "" {
		platform = Platform(runtime.GOOS)
	}

	root, err := resolveRoot(platform, options)
	if err != nil {
		return Paths{}, err
	}

	if isFilesystemRoot(root) {
		return Paths{}, apperrors.New(apperrors.PlatformStatePathUnsafe, errors.New("state root cannot be a filesystem root"))
	}

	workingDirectory := ""
	if strings.TrimSpace(options.WorkingDirectory) != "" {
		workingDirectory, err = cleanAbsolutePath(platform, options.WorkingDirectory)
		if err != nil {
			return Paths{}, apperrors.New(apperrors.PlatformStatePathInvalid, err)
		}
	}
	repositoryRoot, err := resolveRepositoryRoot(platform, options, workingDirectory, fileSystemOrDefault(options.FileSystem))
	if err != nil {
		return Paths{}, err
	}
	if repositoryRoot != "" && pathWithin(root, repositoryRoot) {
		return Paths{}, apperrors.New(apperrors.PlatformStatePathUnsafe, errors.New("state root is inside the repository"))
	}

	ownerRoot, err := resolveOwnerRoot(platform, options)
	if err != nil {
		return Paths{}, err
	}
	ownerRuntime := filepath.Join(ownerRoot, runtimeDirectory)
	if repositoryRoot != "" && pathWithin(ownerRuntime, repositoryRoot) {
		return Paths{}, apperrors.New(apperrors.PlatformStatePathUnsafe, errors.New("owner runtime is inside the repository"))
	}

	return Paths{
		Root:         root,
		Runtime:      filepath.Join(root, runtimeDirectory),
		LockFile:     filepath.Join(ownerRuntime, lockFileName),
		MetadataFile: filepath.Join(ownerRuntime, metadataFileName),
		DatabaseFile: filepath.Join(root, "codex-folio.sqlite3"),
		VaultFile:    filepath.Join(root, vaultFileName),
		ManagedHomes: filepath.Join(root, managedHomesName),
	}, nil
}

func resolveRoot(platform Platform, options PathOptions) (string, error) {
	if options.StateRootOverride != nil {
		raw := strings.TrimSpace(*options.StateRootOverride)
		if raw == "" {
			return "", apperrors.New(apperrors.PlatformStatePathInvalid, errors.New("state root override is empty"))
		}
		root, err := cleanAbsolutePath(platform, raw)
		if err != nil {
			return "", apperrors.New(apperrors.PlatformStatePathInvalid, err)
		}
		return root, nil
	}
	return resolveDefaultRoot(platform, options)
}

func resolveDefaultRoot(platform Platform, options PathOptions) (string, error) {
	home := strings.TrimSpace(options.HomeDir)
	if home == "" {
		return "", apperrors.New(apperrors.PlatformStatePathInvalid, errors.New("user home is unavailable"))
	}
	home, err := cleanAbsolutePath(platform, home)
	if err != nil {
		return "", apperrors.New(apperrors.PlatformStatePathInvalid, err)
	}

	environment := options.Environment
	lookup := func(name string) string {
		if environment == nil {
			return os.Getenv(name)
		}
		return environment[name]
	}

	var root string
	switch platform {
	case PlatformLinux:
		base := strings.TrimSpace(lookup("XDG_DATA_HOME"))
		if base == "" {
			base = filepath.Join(home, ".local", "share")
		} else {
			base, err = cleanAbsolutePath(platform, base)
			if err != nil {
				return "", apperrors.New(apperrors.PlatformStatePathInvalid, err)
			}
		}
		root = filepath.Join(base, linuxDirectoryName)
	case PlatformWindows:
		base := strings.TrimSpace(lookup("LOCALAPPDATA"))
		if base == "" {
			base = filepath.Join(home, "AppData", "Local")
		} else {
			base, err = cleanAbsolutePath(platform, base)
			if err != nil {
				return "", apperrors.New(apperrors.PlatformStatePathInvalid, err)
			}
		}
		root = filepath.Join(base, appDirectoryName)
	case PlatformDarwin:
		root = filepath.Join(home, "Library", "Application Support", appDirectoryName)
	default:
		return "", apperrors.New(apperrors.PlatformStatePathInvalid, fmt.Errorf("unsupported platform %q", platform))
	}
	return cleanAbsolutePath(platform, root)
}

func resolveOwnerRoot(platform Platform, options PathOptions) (string, error) {
	ownerHome := strings.TrimSpace(options.OwnerHomeDir)
	if ownerHome == "" {
		currentUser, err := user.Current()
		if err != nil || currentUser == nil || strings.TrimSpace(currentUser.HomeDir) == "" {
			return "", apperrors.New(apperrors.PlatformStatePathInvalid, errors.New("current user home is unavailable"))
		}
		ownerHome = currentUser.HomeDir
	}
	ownerOptions := options
	ownerOptions.HomeDir = ownerHome
	ownerOptions.Environment = map[string]string{}
	return resolveDefaultRoot(platform, ownerOptions)
}

func cleanAbsolutePath(platform Platform, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("path is empty")
	}
	cleaned := filepath.Clean(raw)
	if !isAbsolutePath(platform, cleaned) {
		return "", errors.New("path must be absolute")
	}
	return cleaned, nil
}

func isAbsolutePath(platform Platform, path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	// Keep simulated Windows path tests independent of the host OS while
	// preventing a Windows path from becoming a relative Unix path.
	if platform == PlatformWindows && len(path) >= 3 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return true
	}
	return platform == PlatformWindows && strings.HasPrefix(path, `\\`)
}

func isFilesystemRoot(path string) bool {
	cleaned := filepath.Clean(path)
	if cleaned == string(filepath.Separator) || cleaned == `\\` {
		return true
	}
	if volume := filepath.VolumeName(cleaned); volume != "" {
		return cleaned == volume+string(filepath.Separator) || cleaned == volume+`\\`
	}
	return len(cleaned) == 3 && cleaned[1] == ':' && (cleaned[2] == '\\' || cleaned[2] == '/')
}

func resolveRepositoryRoot(platform Platform, options PathOptions, workingDirectory string, filesystem FileSystem) (string, error) {
	if options.RepositoryRoot != "" {
		root, err := cleanAbsolutePath(platform, options.RepositoryRoot)
		if err != nil {
			return "", apperrors.New(apperrors.PlatformStatePathInvalid, err)
		}
		return root, nil
	}
	if workingDirectory == "" {
		return "", nil
	}

	current := workingDirectory
	for {
		if _, err := filesystem.Stat(filepath.Join(current, ".git")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", nil
		}
		current = parent
	}
}

func pathWithin(candidate, parent string) bool {
	relative, err := filepath.Rel(parent, candidate)
	if err != nil {
		return false
	}
	if relative == "." {
		return true
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
