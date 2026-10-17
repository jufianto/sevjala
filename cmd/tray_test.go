package cmd

import (
	"bytes"
	"strings"
	"testing"

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
