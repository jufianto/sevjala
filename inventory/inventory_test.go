package inventory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var discard = slog.New(slog.DiscardHandler)

const testID = "BK-20261013-101500-A8F2"

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir(), 0, discard)
	if err != nil {
		t.Fatal(err)
	}
	s.free = func(string) (int64, error) { return 1 << 40, nil }
	return s
}

func manifestFor(id string, data []byte) Manifest {
	sum := sha256.Sum256(data)
	return Manifest{V: 1, BackupID: id, Database: "shop", CreatedAt: time.Date(2026, 10, 13, 10, 15, 0, 0, time.UTC),
		SourceAgent: "c960acf5", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Tags: []string{"manual"}}
}

func receive(s *Store, m Manifest, body []byte) error {
	return s.Receive(m, m.SHA256, m.Size, bytes.NewReader(body))
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestReceiveStoresFileAndManifest(t *testing.T) {
	s := newStore(t)
	data := []byte("-- dump\nINSERT INTO t VALUES (1);\n")
	m := manifestFor(testID, data)
	if err := receive(s, m, data); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(s.Dir(), testID+DataExt))
	if !bytes.Equal(got, data) {
		t.Errorf("stored %q", got)
	}
	gm, err := ReadManifest(filepath.Join(s.Dir(), testID+ManifestExt))
	if err != nil || gm.SHA256 != m.SHA256 || gm.Size != m.Size || !gm.CreatedAt.Equal(m.CreatedAt) {
		t.Errorf("manifest %+v, %v", gm, err)
	}
	if n := names(t, s.Dir()); len(n) != 2 {
		t.Errorf("files %v", n)
	}
	inv, err := s.Scan()
	if err != nil || len(inv.Backups) != 1 || inv.Backups[0].State != StateVerified || len(inv.Problems) != 0 {
		t.Errorf("scan %+v, %v", inv, err)
	}
}

func TestReceiveRejectsAndLeavesNothing(t *testing.T) {
	data := []byte("0123456789abcdef")
	tests := []struct {
		name string
		m    func(Manifest) Manifest
		sha  func(Manifest) string
		body []byte
		want error
	}{
		{name: "hash mismatch", body: []byte("0123456789abcdeX"), want: ErrMismatch},
		{name: "short body", body: data[:10], want: ErrMismatch},
		{name: "body longer than the size", body: append(append([]byte{}, data...), 'x'), want: ErrMismatch},
		{name: "path traversal id", m: func(m Manifest) Manifest { m.BackupID = "../x"; return m }, body: data, want: ErrInvalid},
		{name: "lower-case id", m: func(m Manifest) Manifest { m.BackupID = "BK-20261013-101500-a8f2"; return m }, body: data, want: ErrInvalid},
		{name: "headers differ from manifest", sha: func(Manifest) string { return strings.Repeat("0", 64) }, body: data, want: ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			m := manifestFor(testID, data)
			if tt.m != nil {
				m = tt.m(m)
			}
			sha := m.SHA256
			if tt.sha != nil {
				sha = tt.sha(m)
			}
			if err := s.Receive(m, sha, m.Size, bytes.NewReader(tt.body)); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if n := names(t, s.Dir()); len(n) != 0 {
				t.Errorf("left %v", n)
			}
		})
	}
}

type failAfter struct{ r io.Reader }

func (f failAfter) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if err == io.EOF {
		return n, errors.New("connection reset")
	}
	return n, err
}

func TestReceiveAbortedBodyLeavesNothing(t *testing.T) {
	s := newStore(t)
	data := bytes.Repeat([]byte("x"), 1<<20)
	m := manifestFor(testID, data)
	if err := s.Receive(m, m.SHA256, m.Size, failAfter{bytes.NewReader(data[:1000])}); err == nil {
		t.Fatal("aborted body accepted")
	}
	if n := names(t, s.Dir()); len(n) != 0 {
		t.Errorf("left %v", n)
	}
}

type mustNotRead struct{ t *testing.T }

func (r mustNotRead) Read([]byte) (int, error) { r.t.Error("body read"); return 0, io.EOF }

func TestReceiveExisting(t *testing.T) {
	s := newStore(t)
	data := []byte("same backup")
	m := manifestFor(testID, data)
	if err := receive(s, m, data); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(m, m.SHA256, m.Size, mustNotRead{t}); !errors.Is(err, ErrExists) {
		t.Errorf("same hash: %v", err)
	}
	other := manifestFor(testID, []byte("other backup"))
	if err := receive(s, other, []byte("other backup")); !errors.Is(err, ErrConflict) {
		t.Errorf("other hash: %v", err)
	}
}

// A transfer the node still holds (its controller vanished without closing) must
// not fail the retry for good: it frees itself within the 60 s read deadline.
func TestReceiveInFlightIsBusy(t *testing.T) {
	s := newStore(t)
	if !s.claim(testID) {
		t.Fatal("claim")
	}
	data := []byte("x")
	if err := receive(s, manifestFor(testID, data), data); !errors.Is(err, ErrBusy) || errors.Is(err, ErrConflict) {
		t.Errorf("err = %v", err)
	}
}

func TestReceiveNoSpace(t *testing.T) {
	s := newStore(t)
	s.minFree = 50
	s.free = func(string) (int64, error) { return 100, nil }
	data := bytes.Repeat([]byte("x"), 60)
	if err := receive(s, manifestFor(testID, data), data); !errors.Is(err, ErrNoSpace) ||
		err.Error() != "not enough free space: 100 B free, the backup needs 60 B and 50 B must stay free" {
		t.Errorf("err = %v", err)
	}
}

func TestScanStates(t *testing.T) {
	s := newStore(t)
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(s.Dir(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ok := manifestFor("BK-20261013-100000-0001", []byte("ok"))
	write(ok.BackupID+DataExt, []byte("ok"))
	if err := WriteManifest(filepath.Join(s.Dir(), ok.BackupID+ManifestExt), ok); err != nil {
		t.Fatal(err)
	}
	short := manifestFor("BK-20261013-100000-0002", []byte("long data"))
	write(short.BackupID+DataExt, []byte("long"))
	_ = WriteManifest(filepath.Join(s.Dir(), short.BackupID+ManifestExt), short)
	alone := manifestFor("BK-20261013-100000-0003", []byte("gone"))
	_ = WriteManifest(filepath.Join(s.Dir(), alone.BackupID+ManifestExt), alone)
	write("BK-20261013-100000-0004"+DataExt, []byte("orphan"))
	write("notes.txt", []byte("not ours"))

	inv, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"BK-20261013-100000-0001": StateVerified, "BK-20261013-100000-0002": StateCorrupt,
		"BK-20261013-100000-0003": StateCorrupt, "BK-20261013-100000-0004": StateCorrupt}
	if len(inv.Backups) != len(want) {
		t.Fatalf("backups %+v", inv.Backups)
	}
	for _, c := range inv.Backups {
		if want[c.BackupID] != c.State {
			t.Errorf("%s = %s, want %s", c.BackupID, c.State, want[c.BackupID])
		}
	}
	if len(inv.Problems) != 3 {
		t.Errorf("problems %q", inv.Problems)
	}
}

func TestScanRemovesStalePartOnce(t *testing.T) {
	s := newStore(t)
	stale := filepath.Join(s.Dir(), "BK-20261013-100000-0001"+PartExt)
	fresh := filepath.Join(s.Dir(), "BK-20261013-100000-0002"+PartExt)
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(stale, old, old)
	inv, _ := s.Scan()
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Error("stale .part kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh .part removed")
	}
	if len(inv.Problems) != 1 || !strings.Contains(inv.Problems[0], "removed partial transfer") {
		t.Errorf("problems %q", inv.Problems)
	}
	if again, _ := s.Scan(); len(again.Problems) != 0 {
		t.Errorf("reported twice: %q", again.Problems)
	}
}

func TestOpenRemovesLeftovers(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"BK-20261013-100000-0001" + PartExt, "BK-20261013-100000-0001" + ManifestExt + ".tmp"} {
		_ = os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600)
	}
	s, err := Open(dir, 0, discard)
	if err != nil {
		t.Fatal(err)
	}
	if n := names(t, dir); len(n) != 0 {
		t.Errorf("left %v", n)
	}
	if inv, _ := s.Scan(); len(inv.Problems) != 2 {
		t.Errorf("problems %q", inv.Problems)
	}
}

func TestNewID(t *testing.T) {
	id := NewID(time.Date(2026, 10, 13, 10, 15, 0, 0, time.Local))
	if !ValidID(id) || !strings.HasPrefix(id, "BK-20261013-101500-") {
		t.Errorf("NewID = %q", id)
	}
}

func TestFreeBytes(t *testing.T) {
	if n, err := FreeBytes(t.TempDir()); err != nil || n <= 0 {
		t.Errorf("FreeBytes = %d, %v", n, err)
	}
}

func TestDelete(t *testing.T) {
	data := []byte("to be deleted")
	tests := []struct {
		name    string
		remove  []string // files removed before the delete
		deleted bool
	}{
		{"both files", nil, true},
		{"data only", []string{ManifestExt}, true},
		{"manifest only", []string{DataExt}, true},
		{"absent", []string{DataExt, ManifestExt}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			if err := receive(s, manifestFor(testID, data), data); err != nil {
				t.Fatal(err)
			}
			for _, ext := range tt.remove {
				os.Remove(s.path(testID, ext))
			}
			deleted, err := s.Delete(testID)
			if err != nil || deleted != tt.deleted {
				t.Errorf("Delete = %v, %v", deleted, err)
			}
			if left := names(t, s.Dir()); len(left) != 0 {
				t.Errorf("left %v", left)
			}
		})
	}
}

func TestDeleteRefuses(t *testing.T) {
	s := newStore(t)
	if _, err := s.Delete("../config"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad id: %v", err)
	}
	if !s.claim(testID) {
		t.Fatal("claim")
	}
	if _, err := s.Delete(testID); !errors.Is(err, ErrBusy) {
		t.Errorf("in flight: %v", err)
	}
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1 << 30, "1.0 GiB"},
		{1288490189, "1.2 GiB"},
		{5 << 30, "5.0 GiB"},
		{1 << 62, "4.0 EiB"},
	}
	for _, tt := range tests {
		if got := HumanBytes(tt.n); got != tt.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// Export writes manifests into folders other accounts may write in; a link
// planted at the temp name must not redirect the write.
func TestWriteFileDoesNotFollowALinkAtItsTempName(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "BK-1"+ManifestExt)
	if err := os.Symlink(victim, path+".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "keep" {
		t.Errorf("the link's target was overwritten: %q", got)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Errorf("file %q", got)
	}
}
