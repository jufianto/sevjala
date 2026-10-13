package dump

import (
	"os"
	"syscall"
	"testing"
)

func TestDiskFullWindows(t *testing.T) {
	for _, e := range []syscall.Errno{112, 39} {
		err := explain("mysqldump", Conn{}, "", "", &os.PathError{Op: "write", Path: `C:\x`, Err: e})
		if err.Error() != "not enough disk space to write the dump" {
			t.Errorf("errno %d: %v", e, err)
		}
	}
}
