package svc

import (
	"strings"
)

// startMenuCommand makes the Start menu entry through WScript.Shell, which
// every Windows since 7 has; Go has no shortcut writer of its own.
func startMenuCommand(lnk, exe string) []string {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	dir := exe
	if i := strings.LastIndex(exe, `\`); i > 0 {
		dir = exe[:i]
	}
	script := "$s=(New-Object -ComObject WScript.Shell).CreateShortcut(" + q(lnk) + ");" +
		"$s.TargetPath=" + q(exe) + ";$s.Arguments='tray --show';$s.IconLocation=" + q(exe+",0") + ";" +
		"$s.WorkingDirectory=" + q(dir) + ";$s.WindowStyle=7;" +
		"$s.Description='Sevjala: backup status and the dashboard';$s.Save()"
	return []string{"powershell", "-NoProfile", "-NonInteractive", "-Command", script}
}
