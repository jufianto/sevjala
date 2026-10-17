package svc

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// The Run key starts the tray at every user's login; the Start menu entry
// brings it back after "Quit tray".
const (
	runKey       = `Software\Microsoft\Windows\CurrentVersion\Run`
	trayRunValue = "Sevjala Tray"
)

func startMenuLink() string {
	return filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs`, "Sevjala.lnk")
}

func InstallTrayAutostart(exe string) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetStringValue(trayRunValue, `"`+exe+`" tray`); err != nil {
		return err
	}
	c := startMenuCommand(startMenuLink(), exe)
	if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
		return fmt.Errorf("the Start menu entry could not be made: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func RemoveTrayAutostart() error {
	if err := os.Remove(startMenuLink()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(trayRunValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

func TrayAutostartInstalled() bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(trayRunValue)
	return err == nil
}
