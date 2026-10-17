package logx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLog(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
}

func entry(n string) string { return `{"msg":"` + n + `"}` }

func msgs(t *testing.T, es []json.RawMessage) []string {
	t.Helper()
	var out []string
	for _, e := range es {
		var v struct{ Msg string }
		if err := json.Unmarshal(e, &v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v.Msg)
	}
	return out
}

func TestTailNewestFirstAcrossRotations(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, FileName)
	writeLog(t, p+".2", entry("e1"), entry("e2")+"\n")
	writeLog(t, p+".1", entry("e3")+"\n")
	writeLog(t, p, entry("e4"), entry("e5")+"\n")
	es, err := Tail(dir, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(msgs(t, es), ","); got != "e5,e4,e3,e2" {
		t.Errorf("got %s", got)
	}
}

func TestTailSkipsBrokenLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, FileName)
	writeLog(t, p+".3", entry("old")+"\n")
	writeLog(t, p+".1", entry("a"), "not json", entry("b")+"\n")
	writeLog(t, p, entry("c"), `{"msg":"half`)
	es, err := Tail(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(msgs(t, es), ","); got != "c,b,a,old" {
		t.Errorf("got %s", got)
	}
}

func TestTailEmptyDir(t *testing.T) {
	es, err := Tail(t.TempDir(), 5)
	if err != nil || len(es) != 0 {
		t.Errorf("%v, %v", es, err)
	}
}

func TestTailAcrossChunkEdges(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, FileName)
	writeLog(t, p+".1", entry("a"), "", entry("bb")+"\n")
	writeLog(t, p, entry("ccc"), "not json", entry("d"), `{"msg":"half`)
	defer func(n int64) { tailChunk = n }(tailChunk)
	for _, n := range []int64{1, 2, 3, 7, 11, 12, 13, 64, 1 << 16} {
		tailChunk = n
		for limit, want := range []string{"", "d", "d,ccc", "d,ccc,bb", "d,ccc,bb,a", "d,ccc,bb,a"} {
			es, err := Tail(dir, limit)
			if got := strings.Join(msgs(t, es), ","); err != nil || got != want {
				t.Errorf("chunk %d, limit %d: got %q, %v; want %q", n, limit, got, err, want)
			}
		}
	}
}
