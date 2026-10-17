//go:build darwin || linux

package svc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrayAutostart(t *testing.T) {
	trayAutostartPath = filepath.Join(t.TempDir(), "autostart", "sevjala-tray")
	if TrayAutostartInstalled() {
		t.Fatal("installed before install")
	}
	if err := InstallTrayAutostart("/usr/local/bin/sevjala"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(trayAutostartPath)
	if err != nil || !strings.Contains(string(b), "/usr/local/bin/sevjala") || !strings.Contains(string(b), "tray") || !TrayAutostartInstalled() {
		t.Errorf("%q, %v", b, err)
	}
	if err := RemoveTrayAutostart(); err != nil || TrayAutostartInstalled() {
		t.Errorf("remove: %v", err)
	}
	if err := RemoveTrayAutostart(); err != nil {
		t.Errorf("removing twice: %v", err)
	}
}
