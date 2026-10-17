package svc

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// trayAutostartPath is a LaunchAgent: launchd starts the tray in every user's
// GUI session at login.
var trayAutostartPath = "/Library/LaunchAgents/dev.jufi.sevjala.tray.plist"

func InstallTrayAutostart(exe string) error {
	var q bytes.Buffer
	_ = xml.EscapeText(&q, []byte(exe))
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>dev.jufi.sevjala.tray</string>
	<key>ProgramArguments</key><array><string>` + q.String() + `</string><string>tray</string></array>
	<key>RunAtLoad</key><true/>
	<key>LimitLoadToSessionType</key><string>Aqua</string>
</dict>
</plist>
`
	if err := os.MkdirAll(filepath.Dir(trayAutostartPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(trayAutostartPath, []byte(plist), 0o644)
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
