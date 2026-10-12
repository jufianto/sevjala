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
