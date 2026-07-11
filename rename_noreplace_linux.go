//go:build linux

package main

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func renameNoReplace(oldPath, newPath string) error {
	err := unix.Renameat2(unix.AT_FDCWD, oldPath, unix.AT_FDCWD, newPath, unix.RENAME_NOREPLACE)
	if err == unix.ENOSYS || err == unix.EINVAL || err == unix.EOPNOTSUPP {
		return fmt.Errorf("atomic no-clobber rename is unsupported by this kernel or filesystem: %w", err)
	}
	return err
}
