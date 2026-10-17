package tray

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// As a child of TestNewConsoleIsTheChildsConsole, report whether stdout is a console.
func TestMain(m *testing.M) {
	if out := os.Getenv("SEVJALA_CONSOLE_PROBE"); out != "" {
		var mode uint32
		got := "console"
		if windows.GetConsoleMode(windows.Stdout, &mode) != nil {
			got = "not a console"
		}
		_ = os.WriteFile(out, []byte(got), 0o600)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// "Open status" runs 'sevjala status' in a window of its own, which waits for
// Enter; that only works when the window's console is its stdin and stdout.
func TestNewConsoleIsTheChildsConsole(t *testing.T) {
	out := filepath.Join(t.TempDir(), "probe")
	t.Setenv("SEVJALA_CONSOLE_PROBE", out)
	if err := newConsole(os.Args[0], "-test.run=^$"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(out); err == nil && len(b) > 0 {
			if string(b) != "console" {
				t.Fatalf("the child's stdout is %s", b)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the child never reported")
}
