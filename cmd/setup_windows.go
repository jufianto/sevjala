//go:build windows

package cmd

import (
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const exeName = "sevjala.exe"

var (
	procGetConsoleProcessList = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList")
	procSendMessageTimeout    = windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
)

// ownConsole reports a double-click: Explorer gave the process a console of
// its own, while a terminal shares its console with the shell.
func ownConsole() bool {
	var ids [2]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&ids[0])), uintptr(len(ids)))
	return n == 1
}

func installDir() string {
	pf := os.Getenv("ProgramFiles")
	if pf == "" {
		pf = `C:\Program Files`
	}
	return filepath.Join(pf, "Sevjala")
}

// elevate starts setup again through the UAC prompt; the new window carries
// on and this one exits.
func elevate(g *globals) error {
	exe, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	args := "setup"
	if g.dataDirFlag != "" {
		args += ` --data-dir "` + g.dataDirFlag + `"`
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	argp, _ := windows.UTF16PtrFromString(args)
	if err := windows.ShellExecute(0, verb, file, argp, nil, windows.SW_SHOWNORMAL); err != nil {
		return failf("administrator rights are needed to install (%w)", err)
	}
	os.Exit(0)
	return nil
}

const envKey = `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`

// setPath adds dir to, or removes it from, the machine PATH and tells running
// programs; new prompts see the change.
func setPath(add bool, dir string) error {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, envKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	cur, _, err := k.GetStringValue("Path")
	if err != nil {
		return err
	}
	next, changed := pathList(cur, dir, add)
	if !changed {
		return nil
	}
	if err := k.SetExpandStringValue("Path", next); err != nil {
		return err
	}
	env, _ := windows.UTF16PtrFromString("Environment")
	const hwndBroadcast, wmSettingChange, smtoAbortIfHung = 0xffff, 0x1a, 0x2
	_, _, _ = procSendMessageTimeout.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, 0)
	return nil
}
