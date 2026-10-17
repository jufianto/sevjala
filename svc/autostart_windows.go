package svc

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

// The Run key starts the tray at every user's login.
const (
	runKey       = `Software\Microsoft\Windows\CurrentVersion\Run`
	trayRunValue = "Sevjala Tray"
)

func InstallTrayAutostart(exe string) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(trayRunValue, `"`+exe+`" tray`)
}

func RemoveTrayAutostart() error {
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
