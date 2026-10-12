//go:build !windows

package svc

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// LockDataDir makes dir root-only. It refuses a dir someone else owns or a
// symlink, since either could carry a planted config (decisions.md D-010).
func LockDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("refusing %s: it is a symlink; use the real path", dir)
	}
	if uid := fi.Sys().(*syscall.Stat_t).Uid; uid != 0 {
		return fmt.Errorf("refusing %s: owned by uid %d, not root; remove it or 'chown -R root' it first", dir, uid)
	}
	if err := os.Chown(dir, 0, 0); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

func CheckDataDirLocked(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if uid := fi.Sys().(*syscall.Stat_t).Uid; uid != 0 {
		return fmt.Errorf("%s is owned by uid %d, not root", dir, uid)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s has mode %v; want 0700", dir, perm)
	}
	return nil
}
