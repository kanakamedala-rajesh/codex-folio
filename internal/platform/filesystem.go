package platform

import (
	"errors"
	"io"
	"os"
)

// File is the small file handle surface needed by the state-owner adapter.
// The production implementation is *os.File; tests can provide deterministic
// handles without changing ownership policy.
type File interface {
	io.Reader
	io.Writer
	io.Closer
	Stat() (os.FileInfo, error)
	Chmod(os.FileMode) error
	Sync() error
}

// FileSystem is the filesystem and locking seam used by local ownership.
// Production uses osFileSystem; tests can inject failures and contention at
// the same boundary as the native adapter.
type FileSystem interface {
	Lstat(string) (os.FileInfo, error)
	Stat(string) (os.FileInfo, error)
	MkdirAll(string, os.FileMode) error
	Chmod(string, os.FileMode) error
	OpenFile(string, int, os.FileMode) (File, error)
	Open(string) (File, error)
	Remove(string) error
	Rename(string, string) error
	EnforcePrivatePermissions(string) error
	TryExclusiveLock(File) error
	ReleaseExclusiveLock(File) error
	IsLockContention(error) bool
}

type osFileSystem struct{}

func (osFileSystem) Lstat(path string) (os.FileInfo, error) {
	return os.Lstat(path)
}

func (osFileSystem) Stat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

func (osFileSystem) MkdirAll(path string, permission os.FileMode) error {
	return os.MkdirAll(path, permission)
}

func (osFileSystem) Chmod(path string, permission os.FileMode) error {
	return os.Chmod(path, permission)
}

func (osFileSystem) OpenFile(path string, flags int, permission os.FileMode) (File, error) {
	return os.OpenFile(path, flags, permission)
}

func (osFileSystem) Open(path string) (File, error) {
	return os.Open(path)
}

func (osFileSystem) Remove(path string) error {
	return os.Remove(path)
}

func (osFileSystem) Rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}

func (osFileSystem) EnforcePrivatePermissions(path string) error {
	return enforcePrivatePermissions(path)
}

func (osFileSystem) TryExclusiveLock(file File) error {
	native, ok := file.(*os.File)
	if !ok {
		return errors.New("native lock requires an operating-system file")
	}
	return tryExclusiveLock(native)
}

func (osFileSystem) ReleaseExclusiveLock(file File) error {
	native, ok := file.(*os.File)
	if !ok {
		return errors.New("native unlock requires an operating-system file")
	}
	return releaseExclusiveLock(native)
}

func (osFileSystem) IsLockContention(err error) bool {
	return lockContention(err)
}

func fileSystemOrDefault(filesystem FileSystem) FileSystem {
	if filesystem == nil {
		return osFileSystem{}
	}
	return filesystem
}
