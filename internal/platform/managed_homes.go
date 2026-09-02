package platform

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

// ManagedHomeProvisioner creates only private directories below the
// app-local managed-home boundary. The returned path is for internal launch
// and Codex authentication use; callers should not put it in ordinary output.
type ManagedHomeProvisioner struct {
	root       string
	filesystem FileSystem
}

func NewManagedHomeProvisioner(root string) (*ManagedHomeProvisioner, error) {
	return NewManagedHomeProvisionerWithFileSystem(root, nil)
}

func NewManagedHomeProvisionerWithFileSystem(root string, filesystem FileSystem) (*ManagedHomeProvisioner, error) {
	root = strings.TrimSpace(root)
	if root == "" || !filepath.IsAbs(root) || isFilesystemRoot(filepath.Clean(root)) {
		return nil, apperrors.New(apperrors.PlatformStatePathInvalid, errors.New("managed home root is invalid"))
	}
	if filesystem == nil {
		filesystem = osFileSystem{}
	}
	return &ManagedHomeProvisioner{root: filepath.Clean(root), filesystem: filesystem}, nil
}

func (provisioner *ManagedHomeProvisioner) Ensure(ctx context.Context, profileID string) (string, error) {
	if provisioner == nil || provisioner.filesystem == nil {
		return "", apperrors.New(apperrors.PlatformServiceUnavailable, errors.New("managed home provisioner is unavailable"))
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", apperrors.New(apperrors.PlatformServiceUnavailable, err)
		}
	}
	if profileID == "" || profileID == "." || profileID == ".." || strings.ContainsAny(profileID, `/\\`) || filepath.Base(profileID) != profileID {
		return "", apperrors.New(apperrors.PlatformStatePathUnsafe, errors.New("managed home identifier is unsafe"))
	}
	if err := ensurePrivateDirectory(provisioner.filesystem, provisioner.root); err != nil {
		return "", err
	}
	home := filepath.Join(provisioner.root, profileID)
	if err := ensurePrivateDirectory(provisioner.filesystem, home); err != nil {
		return "", err
	}
	return home, nil
}
