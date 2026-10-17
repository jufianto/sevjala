package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/tray"
)

func TestTrayWithoutTraySupportSaysHow(t *testing.T) {
	if tray.Available {
		t.Skip("this build has a tray")
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"tray", "--data-dir", t.TempDir()}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "-tags tray") {
		t.Errorf("exit %d, stderr %q", code, errOut.String())
	}
}

// The Start menu entry runs 'tray --show': with a tray already running it
// opens the dashboard; a plain 'tray' (the start at login) leaves it alone.
func TestTrayShowOpensTheDashboardWhenATrayRuns(t *testing.T) {
	oldRun, oldOpen := runTray, openDashboard
	t.Cleanup(func() { runTray, openDashboard = oldRun, oldOpen })
	runTray = func(*localapi.Client, string, func() bool) error { return tray.ErrAlreadyRunning }
	for _, tt := range []struct {
		args   []string
		opened bool
	}{
		{[]string{"tray", "--show"}, true},
		{[]string{"tray"}, false},
	} {
		opened := false
		openDashboard = func(string) error { opened = true; return nil }
		var out, errOut bytes.Buffer
		if code := run(append(tt.args, "--data-dir", t.TempDir()), &out, &errOut); code != 0 || opened != tt.opened {
			t.Errorf("%v: exit %d, dashboard opened %v, stderr %q", tt.args, code, opened, errOut.String())
		}
	}
}
