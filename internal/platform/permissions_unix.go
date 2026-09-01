//go:build !windows

package platform

import (
	"errors"
	"os"
	"syscall"
)

func enforcePrivatePermissions(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("filesystem owner information is unavailable")
	}
	if stat.Uid != uint32(os.Getuid()) {
		return errors.New("state path is owned by another user")
	}
	return nil
}
