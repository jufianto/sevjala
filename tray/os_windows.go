package tray

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

var procFreeConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("FreeConsole")

// hideConsole lets go of the console a Run-key start gives a console program,
// so its window closes after a brief flash.
func hideConsole() { _, _, _ = procFreeConsole.Call() }

// singleInstance: one tray per logon session.
func singleInstance() (release func(), first bool, err error) {
	name, _ := windows.UTF16PtrFromString(`Local\SevjalaTray`)
	h, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		_ = windows.CloseHandle(h)
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return func() { _ = windows.CloseHandle(h) }, true, nil
}

// spawn starts exe without a console, so the new tray shows no window.
func spawn(exe string, args ...string) error {
	c := exec.Command(exe, args...)
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
	return c.Start()
}

func canOpenStatus() bool { return true }

// openStatus runs 'sevjala status' in a console of its own, which waits for
// Enter before it closes (cmd Execute).
func openStatus() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	c := exec.Command(exe, "status")
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE}
	return c.Start()
}
