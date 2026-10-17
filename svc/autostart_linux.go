package svc

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// trayAutostartPath is an XDG autostart entry, which desktop sessions start
// at login.
var trayAutostartPath = "/etc/xdg/autostart/sevjala-tray.desktop"

func InstallTrayAutostart(exe string) error {
	entry := "[Desktop Entry]\nType=Application\nName=Sevjala tray\nExec=\"" +
		strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", "$", `\$`).Replace(exe) + "\" tray\nNoDisplay=true\nX-GNOME-Autostart-enabled=true\n"
	if err := os.MkdirAll(filepath.Dir(trayAutostartPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(trayAutostartPath, []byte(entry), 0o644)
}

func RemoveTrayAutostart() error {
	if err := os.Remove(trayAutostartPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func TrayAutostartInstalled() bool {
	_, err := os.Stat(trayAutostartPath)
	return err == nil
}
