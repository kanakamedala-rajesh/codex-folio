package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

type ProfileHomeLifecycle struct {
	managedRoot    string
	quarantineRoot string
	filesystem     ProfileHomeFileSystem
}

type ProfileHomeFileSystem interface {
	Stat(string) (os.FileInfo, error)
	MkdirAll(string, os.FileMode) error
	Rename(string, string) error
	RemoveAll(string) error
}

type osProfileHomeFileSystem struct{}

func (osProfileHomeFileSystem) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }
func (osProfileHomeFileSystem) MkdirAll(path string, mode os.FileMode) error {
	return os.MkdirAll(path, mode)
}
func (osProfileHomeFileSystem) Rename(source, destination string) error {
	return os.Rename(source, destination)
}
func (osProfileHomeFileSystem) RemoveAll(path string) error { return os.RemoveAll(path) }

func NewProfileHomeLifecycle(managedRoot, quarantineRoot string) (*ProfileHomeLifecycle, error) {
	return NewProfileHomeLifecycleWithFileSystem(managedRoot, quarantineRoot, osProfileHomeFileSystem{})
}

func NewProfileHomeLifecycleWithFileSystem(managedRoot, quarantineRoot string, filesystem ProfileHomeFileSystem) (*ProfileHomeLifecycle, error) {
	managedRoot = filepath.Clean(strings.TrimSpace(managedRoot))
	quarantineRoot = filepath.Clean(strings.TrimSpace(quarantineRoot))
	if filesystem == nil || !filepath.IsAbs(managedRoot) || !filepath.IsAbs(quarantineRoot) || managedRoot == quarantineRoot || isFilesystemRoot(managedRoot) || isFilesystemRoot(quarantineRoot) {
		return nil, apperrors.New(apperrors.PlatformStatePathInvalid, errors.New("profile lifecycle roots are invalid"))
	}
	return &ProfileHomeLifecycle{managedRoot: managedRoot, quarantineRoot: quarantineRoot, filesystem: filesystem}, nil
}

func (lifecycle *ProfileHomeLifecycle) Quarantine(ctx context.Context, profileID, source string) error {
	if err := lifecycle.validate(ctx, profileID, source); err != nil {
		return err
	}
	if err := lifecycle.filesystem.MkdirAll(lifecycle.quarantineRoot, 0o700); err != nil {
		return apperrors.New(apperrors.ProfileQuarantineInvalid, err)
	}
	return lifecycle.move(source, filepath.Join(lifecycle.quarantineRoot, profileID))
}

func (lifecycle *ProfileHomeLifecycle) Restore(ctx context.Context, profileID, destination string) error {
	if err := lifecycle.validate(ctx, profileID, destination); err != nil {
		return err
	}
	if err := lifecycle.filesystem.MkdirAll(lifecycle.managedRoot, 0o700); err != nil {
		return apperrors.New(apperrors.ProfileQuarantineInvalid, err)
	}
	return lifecycle.move(filepath.Join(lifecycle.quarantineRoot, profileID), destination)
}

func (lifecycle *ProfileHomeLifecycle) Purge(ctx context.Context, profileID string) error {
	if err := lifecycle.validate(ctx, profileID, filepath.Join(lifecycle.managedRoot, profileID)); err != nil {
		return err
	}
	target := filepath.Join(lifecycle.quarantineRoot, profileID)
	if err := lifecycle.filesystem.RemoveAll(target); err != nil {
		return apperrors.New(apperrors.ProfileQuarantineInvalid, err)
	}
	return nil
}

func (lifecycle *ProfileHomeLifecycle) validate(ctx context.Context, profileID, managedPath string) error {
	if lifecycle == nil {
		return apperrors.New(apperrors.ProfileQuarantineInvalid, errors.New("profile home lifecycle is unavailable"))
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return apperrors.New(apperrors.ProfileQuarantineInvalid, err)
		}
	}
	if profileID == "" || profileID == "." || profileID == ".." || strings.ContainsAny(profileID, `/\\`) || filepath.Base(profileID) != profileID {
		return apperrors.New(apperrors.PlatformStatePathUnsafe, errors.New("profile identifier is unsafe"))
	}
	if !SamePath(filepath.Clean(managedPath), filepath.Join(lifecycle.managedRoot, profileID)) {
		return apperrors.New(apperrors.PlatformStatePathUnsafe, errors.New("managed Identity Home is outside its owned boundary"))
	}
	return nil
}

func (lifecycle *ProfileHomeLifecycle) move(source, destination string) error {
	_, sourceErr := lifecycle.filesystem.Stat(source)
	_, destinationErr := lifecycle.filesystem.Stat(destination)
	sourceExists := sourceErr == nil
	destinationExists := destinationErr == nil
	if sourceExists && destinationExists {
		return apperrors.New(apperrors.ProfileQuarantineInvalid, errors.New("profile home destination already exists"))
	}
	if !sourceExists && destinationExists {
		return nil
	}
	if !sourceExists {
		return apperrors.New(apperrors.ProfileQuarantineInvalid, errors.Join(errors.New("profile home is missing"), sourceErr))
	}
	if destinationErr != nil && !os.IsNotExist(destinationErr) {
		return apperrors.New(apperrors.ProfileQuarantineInvalid, destinationErr)
	}
	if err := lifecycle.filesystem.Rename(source, destination); err != nil {
		return apperrors.New(apperrors.ProfileQuarantineInvalid, err)
	}
	return nil
}
