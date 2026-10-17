//go:build linux || (darwin && cgo && tray)

package tray

import "testing"

func TestSingleInstance(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	release, first, err := singleInstance()
	if err != nil || !first {
		t.Fatalf("first: %v %v", first, err)
	}
	if _, again, err := singleInstance(); err != nil || again {
		t.Errorf("a second tray started: %v %v", again, err)
	}
	release()
	release2, first, err := singleInstance()
	if err != nil || !first {
		t.Fatalf("after release: %v %v", first, err)
	}
	release2()
}
