//go:build !windows

package svc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckDataDirLockedRejectsOpenDir(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0o755)
	if err := CheckDataDirLocked(dir); err == nil {
		t.Error("a 0755 dir (or one not owned by root) passed")
	}
}

func TestLockDataDirRefusesSymlink(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "real")
	os.Mkdir(target, 0o700)
	link := filepath.Join(base, "data")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := LockDataDir(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("err = %v", err)
	}
}

func TestLockDataDirAsRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	dir := filepath.Join(t.TempDir(), "data")
	if err := LockDataDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := CheckDataDirLocked(dir); err != nil {
		t.Error(err)
	}
	foreign := filepath.Join(t.TempDir(), "planted")
	os.Mkdir(foreign, 0o755)
	os.Chown(foreign, 1, 1)
	if err := LockDataDir(foreign); err == nil || !strings.Contains(err.Error(), "owned by uid 1") {
		t.Errorf("foreign dir: %v", err)
	}
	if fi, _ := os.Stat(foreign); fi.Mode().Perm() != 0o755 {
		t.Error("refused dir was modified")
	}
}
