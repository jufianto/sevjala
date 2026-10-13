package inventory

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"testing"
)

func TestListSkipsIncomplete(t *testing.T) {
	s := newStore(t)
	data := []byte("-- dump\n")
	older := manifestFor("BK-20261012-101500-0001", data)
	if err := receive(s, older, data); err != nil {
		t.Fatal(err)
	}
	if err := receive(s, manifestFor(testID, data), data); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"BK-20261013-111500-0002" + DataExt:     "no manifest",
		"BK-20261013-121500-0003" + ManifestExt: "{}",
		"BK-20261013-131500-0004" + PartExt:     "half",
	} {
		if err := os.WriteFile(s.path(name, ""), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	short := manifestFor("BK-20261013-141500-0005", data)
	if err := WriteManifest(s.path(short.BackupID, ManifestExt), short); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.path(short.BackupID, DataExt), data[:3], 0o600); err != nil {
		t.Fatal(err)
	}

	ms, err := List(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].BackupID != older.BackupID || ms[1].BackupID != testID {
		t.Errorf("got %+v", ms)
	}
	if _, err := List(s.path("missing", "")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing dir: %v", err)
	}
}

func TestStoreOpenHoldsTheBackup(t *testing.T) {
	s := newStore(t)
	data := []byte("-- dump\nINSERT INTO t VALUES (1);\n")
	if err := receive(s, manifestFor(testID, data), data); err != nil {
		t.Fatal(err)
	}
	m, rc, err := s.Open(testID)
	if err != nil {
		t.Fatal(err)
	}
	if m.BackupID != testID || m.Size != int64(len(data)) {
		t.Errorf("manifest %+v", m)
	}
	if _, err := s.Delete(testID); !errors.Is(err, ErrBusy) {
		t.Errorf("delete while open: %v", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil || string(got) != string(data) {
		t.Errorf("read %q, %v", got, err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Delete(testID); !ok || err != nil {
		t.Errorf("delete after close: %v, %v", ok, err)
	}
}

func TestOpenBackupRefuses(t *testing.T) {
	s := newStore(t)
	data := []byte("-- dump\n")
	if err := receive(s, manifestFor(testID, data), data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.path(testID, DataExt), []byte("-- dump\nmore"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		id   string
		want error
	}{
		{"../etc/passwd", ErrInvalid},
		{"BK-20261013-999999-FFFF", fs.ErrNotExist},
		{testID, ErrMismatch},
	} {
		if _, f, err := OpenBackup(s.Dir(), tt.id); !errors.Is(err, tt.want) {
			t.Errorf("%s: err = %v, want %v", tt.id, err, tt.want)
			if f != nil {
				f.Close()
			}
		}
	}
	if _, _, err := s.Open(testID); !errors.Is(err, ErrMismatch) || s.busy(testID) {
		t.Errorf("a failed open must release the backup: %v", err)
	}
}
