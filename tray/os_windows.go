package tray

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

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
	return newConsole(exe, "status")
}

// newConsole starts exe in a console window of its own. Go's exec always hands
// the child stdin and stdout (NUL when unset), which hides that window from
// it; CreateProcess without them lets the new console be both.
func newConsole(exe string, args ...string) error {
	cmdline, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{exe}, args...)))
	if err != nil {
		return err
	}
	var si windows.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(nil, cmdline, nil, nil, false, windows.CREATE_NEW_CONSOLE|windows.CREATE_UNICODE_ENVIRONMENT, nil, nil, &si, &pi); err != nil {
		return err
	}
	_ = windows.CloseHandle(pi.Thread)
	return windows.CloseHandle(pi.Process)
}
