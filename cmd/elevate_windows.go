//go:build windows

package cmd

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const elevationHow = "asking Windows for administrator rights…"

var procShellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// shellExecuteInfo is SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	size       uint32
	mask       uint32
	hwnd       uintptr
	verb       *uint16
	file       *uint16
	parameters *uint16
	directory  *uint16
	show       int32
	instApp    uintptr
	idList     uintptr
	class      *uint16
	keyClass   uintptr
	hotKey     uint32
	icon       uintptr
	process    windows.Handle
}

// elevated starts the command again through the UAC prompt. It opens its own
// window, which waits for Enter at the end (Execute), and this one waits for it.
func elevated(args []string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 1, err
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = syscall.EscapeArg(a)
	}
	const seeMaskNoCloseProcess = 0x40
	info := shellExecuteInfo{mask: seeMaskNoCloseProcess, show: windows.SW_SHOWNORMAL}
	info.size = uint32(unsafe.Sizeof(info))
	info.verb, _ = windows.UTF16PtrFromString("runas")
	info.file, _ = windows.UTF16PtrFromString(exe)
	info.parameters, _ = windows.UTF16PtrFromString(strings.Join(quoted, " "))
	if wd, err := os.Getwd(); err == nil {
		info.directory, _ = windows.UTF16PtrFromString(wd)
	}
	if ok, _, err := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return 1, errElevationRefused
		}
		return 1, err
	}
	defer func() { _ = windows.CloseHandle(info.process) }()
	if _, err := windows.WaitForSingleObject(info.process, windows.INFINITE); err != nil {
		return 1, err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.process, &code); err != nil {
		return 1, err
	}
	return int(code), nil
}
