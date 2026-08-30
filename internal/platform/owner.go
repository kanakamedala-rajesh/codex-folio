package platform

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

const ownerMetadataVersion = 1

var (
	ErrOwnerAlreadyRunning     = errors.New("a state owner already holds the service lock")
	ErrOwnerMetadata           = errors.New("service owner metadata is invalid")
	ErrOwnerDifferentStateRoot = errors.New("service owner uses a different state root")
)

// Clock is the small time seam used by ownership metadata. Production uses
// systemClock; tests can supply a fixed clock without changing lock behavior.
type Clock interface {
	Now() time.Time
}

// OwnerOptions contains process, clock, and external filesystem inputs for
// ownership. Only process and clock values are recorded in metadata.
type OwnerOptions struct {
	Clock      Clock
	ProcessID  func() int
	FileSystem FileSystem
}

// OwnerMetadata is the non-sensitive descriptor written while the exclusive
// owner lock is held. The lock, rather than the timestamp, determines liveness.
type OwnerMetadata struct {
	Version   int       `json:"version"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	StateRoot string    `json:"state_root"`
}

// OwnerStatus describes whether a service owner currently holds the lock.
// Metadata is present for a healthy owner and is nil when no owner is running.
type OwnerStatus struct {
	Running  bool
	Metadata *OwnerMetadata
}

// Owner is a process-local handle for the user-scoped state ownership lock.
// Keeping the lock file open for the lifetime of Owner makes process crashes
// recoverable through the operating system's lock release semantics.
type Owner struct {
	lockFile   File
	paths      Paths
	filesystem FileSystem

	metadata OwnerMetadata
	close    sync.Once
	closeErr error
}

// Acquire prepares the app-local layout and acquires its exclusive user-scoped
// owner lock. A second live owner receives a stable contention error; no
// metadata age check can break a held lock.
func Acquire(paths Paths, options OwnerOptions) (*Owner, error) {
	filesystem := fileSystemOrDefault(options.FileSystem)
	if err := ensureStateLayout(paths, filesystem); err != nil {
		return nil, err
	}

	file, err := openOwnerLock(filesystem, paths.LockFile)
	if err != nil {
		return nil, err
	}
	locked := false
	defer func() {
		if !locked {
			_ = file.Close()
		}
	}()

	if err := filesystem.TryExclusiveLock(file); err != nil {
		if filesystem.IsLockContention(err) {
			return nil, apperrors.New(apperrors.PlatformServiceAlreadyRunning, ErrOwnerAlreadyRunning)
		}
		return nil, apperrors.New(apperrors.PlatformServiceUnavailable, err)
	}
	locked = true

	if err := removeOwnerMetadata(filesystem, paths.MetadataFile); err != nil {
		_ = filesystem.ReleaseExclusiveLock(file)
		locked = false
		return nil, err
	}

	clock := options.Clock
	if clock == nil {
		clock = systemClock{}
	}
	processID := options.ProcessID
	if processID == nil {
		processID = os.Getpid
	}
	metadata := OwnerMetadata{
		Version:   ownerMetadataVersion,
		PID:       processID(),
		StartedAt: clock.Now().UTC(),
		StateRoot: paths.Root,
	}
	if metadata.PID <= 0 || metadata.StartedAt.IsZero() {
		_ = filesystem.ReleaseExclusiveLock(file)
		locked = false
		return nil, apperrors.New(apperrors.PlatformServiceUnavailable, errors.New("owner metadata inputs are invalid"))
	}
	if err := writeOwnerMetadata(filesystem, paths.MetadataFile, metadata); err != nil {
		_ = filesystem.ReleaseExclusiveLock(file)
		locked = false
		return nil, err
	}

	return &Owner{lockFile: file, paths: paths, filesystem: filesystem, metadata: metadata}, nil
}

// Discover checks the lock without taking ownership. A free lock proves that
// no service is running; a held lock must also have valid metadata before it
// is reported as healthy.
func Discover(paths Paths, options OwnerOptions) (OwnerStatus, error) {
	filesystem := fileSystemOrDefault(options.FileSystem)
	if err := ensureStateLayout(paths, filesystem); err != nil {
		return OwnerStatus{}, err
	}

	file, err := openOwnerLock(filesystem, paths.LockFile)
	if err != nil {
		return OwnerStatus{}, err
	}
	defer func() { _ = file.Close() }()

	if err := filesystem.TryExclusiveLock(file); err == nil {
		if err := removeOwnerMetadata(filesystem, paths.MetadataFile); err != nil {
			_ = filesystem.ReleaseExclusiveLock(file)
			return OwnerStatus{}, err
		}
		if err := filesystem.ReleaseExclusiveLock(file); err != nil {
			return OwnerStatus{}, apperrors.New(apperrors.PlatformServiceUnavailable, err)
		}
		return OwnerStatus{}, nil
	} else if !filesystem.IsLockContention(err) {
		return OwnerStatus{}, apperrors.New(apperrors.PlatformServiceUnavailable, err)
	}

	metadata, err := readOwnerMetadata(filesystem, paths.MetadataFile)
	if err != nil {
		return OwnerStatus{}, err
	}
	if metadata.StateRoot != paths.Root {
		return OwnerStatus{}, apperrors.New(apperrors.PlatformServiceAlreadyRunning, ErrOwnerDifferentStateRoot)
	}
	return OwnerStatus{Running: true, Metadata: &metadata}, nil
}

// Metadata returns the descriptor captured when the owner acquired its lock.
func (owner *Owner) Metadata() OwnerMetadata {
	return owner.metadata
}

// Close removes the descriptor and releases the lock. It is safe to call more
// than once; the lock is always released even if descriptor cleanup fails.
func (owner *Owner) Close() error {
	owner.close.Do(func() {
		metadataErr := removeOwnerMetadata(owner.filesystem, owner.paths.MetadataFile)
		unlockErr := owner.filesystem.ReleaseExclusiveLock(owner.lockFile)
		closeErr := owner.lockFile.Close()
		owner.closeErr = firstError(metadataErr, unlockErr, closeErr)
		if owner.closeErr != nil && apperrors.Code(owner.closeErr) == "" {
			owner.closeErr = apperrors.New(apperrors.PlatformServiceUnavailable, owner.closeErr)
		}
	})
	return owner.closeErr
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

func ensureStateLayout(paths Paths, filesystem FileSystem) error {
	if strings.TrimSpace(paths.Root) == "" || strings.TrimSpace(paths.Runtime) == "" || strings.TrimSpace(paths.LockFile) == "" || strings.TrimSpace(paths.MetadataFile) == "" {
		return apperrors.New(apperrors.PlatformServiceUnavailable, errors.New("state paths are incomplete"))
	}
	for _, directory := range []string{
		paths.Root,
		paths.Runtime,
		filepath.Dir(paths.LockFile),
	} {
		if err := ensurePrivateDirectory(filesystem, directory); err != nil {
			return err
		}
	}
	return nil
}

func ensurePrivateDirectory(filesystem FileSystem, path string) error {
	if err := rejectSymlinkAncestors(filesystem, path); err != nil {
		return err
	}
	info, err := filesystem.Lstat(path)
	created := false
	if errors.Is(err, os.ErrNotExist) {
		if err := filesystem.MkdirAll(path, 0o700); err != nil {
			return apperrors.New(apperrors.PlatformPermissionDenied, err)
		}
		info, err = filesystem.Lstat(path)
		created = true
	}
	if err != nil {
		return apperrors.New(apperrors.PlatformPermissionDenied, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return apperrors.New(apperrors.PlatformStatePathUnsafe, errors.New("state directory cannot be a symbolic link"))
	}
	if !info.IsDir() {
		return apperrors.New(apperrors.PlatformPermissionDenied, errors.New("state path is not a directory"))
	}
	if created && !isWindows() {
		if err := filesystem.Chmod(path, 0o700); err != nil {
			return apperrors.New(apperrors.PlatformPermissionDenied, err)
		}
		info, err = filesystem.Lstat(path)
		if err != nil {
			return apperrors.New(apperrors.PlatformPermissionDenied, err)
		}
	}
	if !isPrivateDirectoryMode(info.Mode()) {
		return apperrors.New(apperrors.PlatformPermissionDenied, errors.New("state directory permissions are not user-scoped"))
	}
	if err := filesystem.EnforcePrivatePermissions(path); err != nil {
		return apperrors.New(apperrors.PlatformPermissionDenied, err)
	}
	return nil
}

func rejectSymlinkAncestors(filesystem FileSystem, path string) error {
	current := path
	for {
		info, err := filesystem.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return apperrors.New(apperrors.PlatformStatePathUnsafe, errors.New("state path cannot contain a symbolic link"))
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return apperrors.New(apperrors.PlatformPermissionDenied, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func openOwnerLock(filesystem FileSystem, path string) (File, error) {
	info, err := filesystem.Lstat(path)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, apperrors.New(apperrors.PlatformStatePathUnsafe, errors.New("owner lock cannot be a symbolic link"))
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, apperrors.New(apperrors.PlatformPermissionDenied, err)
	}

	file, err := filesystem.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, apperrors.New(apperrors.PlatformPermissionDenied, err)
	}
	info, err = file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, apperrors.New(apperrors.PlatformPermissionDenied, err)
	}
	if !info.Mode().IsRegular() || !isPrivateFileMode(info.Mode()) {
		_ = file.Close()
		return nil, apperrors.New(apperrors.PlatformPermissionDenied, errors.New("owner lock permissions are not user-scoped"))
	}
	if !isWindows() {
		if err := file.Chmod(0o600); err != nil {
			_ = file.Close()
			return nil, apperrors.New(apperrors.PlatformPermissionDenied, err)
		}
	}
	if err := filesystem.EnforcePrivatePermissions(path); err != nil {
		_ = file.Close()
		return nil, apperrors.New(apperrors.PlatformPermissionDenied, err)
	}
	return file, nil
}

func removeOwnerMetadata(filesystem FileSystem, path string) error {
	info, err := filesystem.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return apperrors.New(apperrors.PlatformPermissionDenied, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return apperrors.New(apperrors.PlatformStatePathUnsafe, errors.New("owner metadata cannot be a symbolic link"))
	}
	if !info.Mode().IsRegular() || !isPrivateFileMode(info.Mode()) {
		return apperrors.New(apperrors.PlatformPermissionDenied, errors.New("owner metadata permissions are not user-scoped"))
	}
	if err := filesystem.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return apperrors.New(apperrors.PlatformPermissionDenied, err)
	}
	return nil
}

func writeOwnerMetadata(filesystem FileSystem, path string, metadata OwnerMetadata) error {
	encoded, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return apperrors.New(apperrors.PlatformServiceUnavailable, err)
	}
	encoded = append(encoded, '\n')
	temporary := path + ".tmp-" + strconv.Itoa(metadata.PID)
	_ = filesystem.Remove(temporary)
	file, err := filesystem.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return apperrors.New(apperrors.PlatformPermissionDenied, err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = filesystem.Remove(temporary)
		}
	}()
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return apperrors.New(apperrors.PlatformPermissionDenied, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return apperrors.New(apperrors.PlatformPermissionDenied, err)
	}
	if err := file.Close(); err != nil {
		return apperrors.New(apperrors.PlatformPermissionDenied, err)
	}
	if !isWindows() {
		if err := filesystem.Chmod(temporary, 0o600); err != nil {
			return apperrors.New(apperrors.PlatformPermissionDenied, err)
		}
	}
	if err := filesystem.EnforcePrivatePermissions(temporary); err != nil {
		return apperrors.New(apperrors.PlatformPermissionDenied, err)
	}
	if err := filesystem.Rename(temporary, path); err != nil {
		if removeErr := filesystem.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return apperrors.New(apperrors.PlatformPermissionDenied, removeErr)
		}
		if err := filesystem.Rename(temporary, path); err != nil {
			return apperrors.New(apperrors.PlatformPermissionDenied, err)
		}
	}
	cleanup = false
	return nil
}

func readOwnerMetadata(filesystem FileSystem, path string) (OwnerMetadata, error) {
	info, err := filesystem.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return OwnerMetadata{}, apperrors.New(apperrors.PlatformServiceMetadataInvalid, ErrOwnerMetadata)
		}
		return OwnerMetadata{}, apperrors.New(apperrors.PlatformPermissionDenied, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !isPrivateFileMode(info.Mode()) {
		return OwnerMetadata{}, apperrors.New(apperrors.PlatformServiceMetadataInvalid, ErrOwnerMetadata)
	}
	file, err := filesystem.Open(path)
	if err != nil {
		return OwnerMetadata{}, apperrors.New(apperrors.PlatformServiceMetadataInvalid, ErrOwnerMetadata)
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var metadata OwnerMetadata
	if err := decoder.Decode(&metadata); err != nil || metadata.Version != ownerMetadataVersion || metadata.PID <= 0 || metadata.StartedAt.IsZero() || strings.TrimSpace(metadata.StateRoot) == "" {
		return OwnerMetadata{}, apperrors.New(apperrors.PlatformServiceMetadataInvalid, ErrOwnerMetadata)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return OwnerMetadata{}, apperrors.New(apperrors.PlatformServiceMetadataInvalid, ErrOwnerMetadata)
	}
	return metadata, nil
}

func isPrivateDirectoryMode(mode os.FileMode) bool {
	if isWindows() {
		return true
	}
	return mode.Perm() == 0o700
}

func isPrivateFileMode(mode os.FileMode) bool {
	if isWindows() {
		return true
	}
	return mode.Perm() == 0o600
}

func isWindows() bool {
	return os.PathSeparator == '\\'
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
