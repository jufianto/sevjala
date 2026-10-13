//go:build !windows

package inventory

import "golang.org/x/sys/unix"

// FreeBytes is the space available to this process on dir's filesystem.
func FreeBytes(dir string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
