package logx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	w, err := NewRotatingWriter(path, 100, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"a", "b", "c", "d"} {
		if _, err := w.Write([]byte(strings.Repeat(c, 60))); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{path: "d", path + ".1": "c", path + ".2": "b"}
	for p, c := range want {
		if got := read(t, p); got != strings.Repeat(c, 60) {
			t.Errorf("%s = %.5q…, want %q×60", filepath.Base(p), got, c)
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Errorf("%s.3 exists; keep=3 means current + .1 + .2", FileName)
	}
}

func TestOversizedWriteOnEmptyFileDoesNotRotate(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	w, err := NewRotatingWriter(path, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(strings.Repeat("x", 50))); err != nil {
		t.Fatal(err)
	}
	w.Close()
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Error("rotated an empty file")
	}
}

func TestReopenAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	for _, s := range []string{"one\n", "two\n"} {
		w, err := NewRotatingWriter(path, 100, 3)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(s))
		w.Close()
	}
	if got := read(t, path); got != "one\ntwo\n" {
		t.Errorf("got %q", got)
	}
}

func TestFailedRenameKeepsOldLogs(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	os.WriteFile(path+".1", []byte("old1"), 0o644)
	os.WriteFile(path+".2", []byte("old2"), 0o644)

	rename = func(from, to string) error {
		if from == path {
			return os.ErrPermission
		}
		return os.Rename(from, to)
	}
	t.Cleanup(func() { rename = os.Rename })

	w, err := NewRotatingWriter(path, 100, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"a", "b", "c"} {
		if _, err := w.Write([]byte(strings.Repeat(c, 60))); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()

	if got := read(t, path+".1"); got != "old1" {
		t.Errorf(".1 = %q, want old1 kept", got)
	}
	if got := read(t, path+".2"); got != "old2" {
		t.Errorf(".2 = %q, want old2 kept", got)
	}
	if got := read(t, path); got != strings.Repeat("a", 60)+strings.Repeat("b", 60)+strings.Repeat("c", 60) {
		t.Errorf("current = %.10q…, want all writes appended", got)
	}
}
