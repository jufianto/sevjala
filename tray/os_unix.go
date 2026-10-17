//go:build linux || (darwin && cgo && tray)

package tray

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func hideConsole() {}

// singleInstance: one tray per user, held by a lock on a file in the user's
// runtime folder.
func singleInstance() (release func(), first bool, err error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("sevjala-tray-%d.lock", os.Getuid())), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() { f.Close() }, true, nil
}

func spawn(exe string, args ...string) error {
	c := exec.Command(exe, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return c.Start()
}

func canOpenStatus() bool {
	if runtime.GOOS == "darwin" {
		return true
	}
	_, err := exec.LookPath("x-terminal-emulator")
	return err == nil
}

// openStatus runs 'sevjala status' in a terminal window that waits for Enter.
func openStatus() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	script := shellQuote(exe) + ` status; printf '\nPress Enter to close '; read -r _`
	if runtime.GOOS == "darwin" {
		as := `tell application "Terminal" to do script "` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(script) + `"`
		return exec.Command("osascript", "-e", as, "-e", `tell application "Terminal" to activate`).Start()
	}
	return exec.Command("x-terminal-emulator", "-e", "sh", "-c", script).Start()
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
