package svc

import (
	"slices"
	"strings"
	"testing"
)

// The Start menu entry starts the tray, or opens the dashboard when one runs,
// with the exe's own icon.
func TestStartMenuCommand(t *testing.T) {
	c := startMenuCommand(`C:\ProgramData\Microsoft\Windows\Start Menu\Programs\Sevjala.lnk`, `C:\Program Files\Sevjala\sevjala.exe`)
	if c[0] != "powershell" || !slices.Contains(c, "-NoProfile") || !slices.Contains(c, "-NonInteractive") {
		t.Fatalf("%q", c)
	}
	script := c[len(c)-1]
	for _, want := range []string{
		`CreateShortcut('C:\ProgramData\Microsoft\Windows\Start Menu\Programs\Sevjala.lnk')`,
		`TargetPath='C:\Program Files\Sevjala\sevjala.exe'`,
		`Arguments='tray --show'`,
		`IconLocation='C:\Program Files\Sevjala\sevjala.exe,0'`,
		`WorkingDirectory='C:\Program Files\Sevjala'`,
		`.Save()`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %s:\n%s", want, script)
		}
	}
}

// A quote in a path must not end the PowerShell string.
func TestStartMenuCommandQuotesPaths(t *testing.T) {
	script := startMenuCommand(`C:\Users\O'Neil\Sevjala.lnk`, `C:\O'Neil\sevjala.exe`)
	if s := script[len(script)-1]; !strings.Contains(s, `'C:\Users\O''Neil\Sevjala.lnk'`) || !strings.Contains(s, `'C:\O''Neil\sevjala.exe'`) {
		t.Errorf("%s", s)
	}
}
