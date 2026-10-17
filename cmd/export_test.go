package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/inventory"
)

func heldBackup(id, db string, at time.Time, data []byte) inventory.Manifest {
	sum := sha256.Sum256(data)
	return inventory.Manifest{V: 1, BackupID: id, Database: db, CreatedAt: at, SourceAgent: "c960acf5",
		Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Tags: []string{"daily"}}
}

func TestPickHeld(t *testing.T) {
	day := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	shop1 := heldBackup("BK-20261014-000000-0001", "shop", day, nil)
	hr := heldBackup("BK-20261014-010000-0002", "hr", day.Add(time.Hour), nil)
	shop2 := heldBackup("BK-20261014-020000-0003", "shop", day.Add(2*time.Hour), nil)
	all := []inventory.Manifest{shop1, hr, shop2}

	for _, tt := range []struct {
		held     []inventory.Manifest
		arg, db  string
		want     string
		code     int
		contains string
	}{
		{held: all, arg: "latest", db: "shop", want: shop2.BackupID},
		{held: all, arg: "latest", db: "hr", want: hr.BackupID},
		{held: []inventory.Manifest{shop1, shop2}, arg: "latest", want: shop2.BackupID},
		{held: all, arg: shop1.BackupID, want: shop1.BackupID},
		{held: all, arg: "latest", code: 2, contains: "hr, shop"},
		{held: all, arg: "latest", db: "crm", code: 1, contains: "no backups of crm"},
		{held: nil, arg: "latest", code: 1, contains: "holds no backups"},
		{held: all, arg: "BK-20261013-000000-FFFF", code: 1, contains: "not on this PC"},
	} {
		m, err := pickHeld(tt.held, tt.arg, tt.db)
		var ee *exitError
		switch {
		case tt.want != "" && (err != nil || m.BackupID != tt.want):
			t.Errorf("%s --db %q: got %s, %v; want %s", tt.arg, tt.db, m.BackupID, err, tt.want)
		case tt.want == "" && (!errors.As(err, &ee) || ee.code != tt.code || !strings.Contains(err.Error(), tt.contains)):
			t.Errorf("%s --db %q: err = %v; want code %d containing %q", tt.arg, tt.db, err, tt.code, tt.contains)
		}
	}
}

func TestWriteExport(t *testing.T) {
	data := []byte("-- dump\nINSERT INTO t VALUES (1);\n")
	m := heldBackup("BK-20261014-002000-30D3", "shop", time.Date(2026, 10, 14, 0, 20, 0, 0, time.Local), data)
	dir := t.TempDir()

	path, err := writeExport(m, bytes.NewReader(data), dir, false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "shop", "2026-10-14_00-20-00")
	if got, _ := os.ReadFile(path); !bytes.Equal(got, data) || path != want+inventory.DataExt {
		t.Errorf("wrote %q to %s, want %s", got, path, want+inventory.DataExt)
	}
	if got, err := inventory.ReadManifest(want + inventory.ManifestExt); err != nil || got.SHA256 != m.SHA256 {
		t.Errorf("manifest %+v, %v", got, err)
	}

	if _, err := writeExport(m, bytes.NewReader(data), dir, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("existing file: %v", err)
	}
	if _, err := writeExport(m, bytes.NewReader(data), dir, true); err != nil {
		t.Errorf("--force: %v", err)
	}

	bad := t.TempDir()
	for name, body := range map[string][]byte{"changed": []byte("-- dump\nINSERT INTO t VALUES (2);\n"), "short": data[:5], "long": append(slices.Clone(data), 'x')} {
		if _, err := writeExport(m, bytes.NewReader(body), bad, false); err == nil || !strings.Contains(err.Error(), "nothing was written") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if left, _ := os.ReadDir(bad); len(left) != 0 {
		t.Errorf("a failed export left %v", left)
	}

	if _, err := writeExport(m, bytes.NewReader(data), filepath.Join(dir, "nope"), false); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing folder: %v", err)
	}
}

func TestRenderHeld(t *testing.T) {
	var b bytes.Buffer
	renderHeld(&b, nil, nil)
	if b.String() != "this PC holds no backups yet\n" {
		t.Errorf("empty: %q", b.String())
	}
	b.Reset()
	old := heldBackup("BK-20261001-002000-0001", "shop", time.Date(2026, 10, 1, 0, 20, 0, 0, time.Local), make([]byte, 1024))
	old.SourceAgent = "0d1d0000-2222-4222-8222-222222222222"
	unnamed := heldBackup("BK-20261002-002000-0002", "hr", time.Date(2026, 10, 2, 0, 20, 0, 0, time.Local), make([]byte, 1024))
	unnamed.SourceAgent = "5e5e0000-3333-4333-8333-333333333333"
	renderHeld(&b, []inventory.Manifest{old, unnamed, heldBackup("BK-20261014-002000-30D3", "shop",
		time.Date(2026, 10, 14, 0, 20, 0, 0, time.Local), make([]byte, 2048))},
		[]inventory.Source{{AgentID: "c960acf5", Hostname: "SERVER-01", Backups: 1, Current: true},
			{AgentID: old.SourceAgent, Hostname: "OLD-PC", Backups: 1}, {AgentID: unnamed.SourceAgent, Backups: 1}})
	want := "BACKUP                   DATABASE  FROM               CREATED           SIZE     TAGS\n" +
		"BK-20261001-002000-0001  shop      OLD-PC (former)    2026-10-01 00:20  1.0 KiB  daily\n" +
		"BK-20261002-002000-0002  hr        5e5e0000 (former)  2026-10-02 00:20  1.0 KiB  daily\n" +
		"BK-20261014-002000-30D3  shop      SERVER-01          2026-10-14 00:20  2.0 KiB  daily\n" +
		"\ncopy one out: sevjala backups export <BACKUP> --to <FOLDER>\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
}

func TestExportFolderIsSafe(t *testing.T) {
	for db, want := range map[string]string{"shop": "shop", "a/b": "a_b", `c:\d`: "c__d", "..": "_", "x?y*": "x_y_"} {
		if got := exportFolder(db); got != want {
			t.Errorf("%q: got %q, want %q", db, got, want)
		}
	}
}

func TestExportAll(t *testing.T) {
	day := time.Date(2026, 10, 14, 0, 0, 0, 0, time.Local)
	bodies := map[string][]byte{}
	var held []inventory.Manifest
	for i, db := range []string{"shop", "hr", "shop"} {
		data := []byte(fmt.Sprintf("-- dump %d\n", i))
		m := heldBackup(fmt.Sprintf("BK-20261014-00%02d00-000%d", i*10, i), db, day.Add(time.Duration(i)*10*time.Minute), data)
		bodies[m.BackupID] = data
		held = append(held, m)
	}
	opened := 0
	open := func(_ context.Context, id string) (io.ReadCloser, error) {
		opened++
		if id == "BK-20261014-001000-0001" && opened > 3 {
			return nil, errors.New("connection dropped")
		}
		return io.NopCloser(bytes.NewReader(bodies[id])), nil
	}
	dir := t.TempDir()

	var out bytes.Buffer
	if err := exportAll(context.Background(), held, "", open, dir, false, &out); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"shop/2026-10-14_00-00-00.sql.gz", "hr/2026-10-14_00-10-00.sql.gz", "shop/2026-10-14_00-20-00.sql.gz"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if !strings.HasSuffix(out.String(), "exported 3, skipped 0 already there\n") {
		t.Errorf("first run:\n%s", out.String())
	}

	out.Reset()
	if err := exportAll(context.Background(), held, "", open, dir, false, &out); err != nil || opened != 3 {
		t.Errorf("second run opened %d streams, err %v", opened, err)
	}
	if !strings.HasSuffix(out.String(), "exported 0, skipped 3 already there\n") {
		t.Errorf("second run:\n%s", out.String())
	}

	out.Reset()
	err := exportAll(context.Background(), held, "", open, dir, true, &out)
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != 1 || !strings.Contains(out.String(), filepath.Join("hr", "2026-10-14_00-10-00.sql.gz")+": failed") ||
		!strings.Contains(out.String(), "exported 2, skipped 0 already there, 1 failed") {
		t.Errorf("--force with one failure: %v\n%s", err, out.String())
	}

	out.Reset()
	if err := exportAll(context.Background(), held, "crm", open, dir, false, &out); err == nil || !strings.Contains(err.Error(), "no backups of crm") {
		t.Errorf("unknown db: %v", err)
	}
}

func TestExportAsksForAdminFirst(t *testing.T) {
	got := fakeElevation(t, 0, nil)
	args := []string{"backups", "export", "latest", "--to", t.TempDir(), "--data-dir", t.TempDir()}
	var out, errOut bytes.Buffer
	if code := run(args, &out, &errOut); code != 0 || len(*got) == 0 {
		t.Errorf("exit %d, elevated %q, stderr %q", code, *got, errOut.String())
	}
	if !strings.Contains(errOut.String(), "exporting a backup needs administrator rights") {
		t.Errorf("stderr %q", errOut.String())
	}
}

// Under sudo the copy is handed to the user who ran it, not left to root.
func TestSudoOwner(t *testing.T) {
	t.Setenv("SUDO_UID", "501")
	t.Setenv("SUDO_GID", "20")
	if uid, gid, ok := sudoOwner(); !ok || uid != 501 || gid != 20 {
		t.Errorf("got %d %d %v", uid, gid, ok)
	}
	t.Setenv("SUDO_UID", "")
	if _, _, ok := sudoOwner(); ok {
		t.Error("not under sudo")
	}
}

// Under sudo, a folder that was already there keeps its owner: only what the
// export made is handed over, and a link is never followed.
func TestExportUnderSudoHandsOverOnlyWhatItMade(t *testing.T) {
	old := lchown
	t.Cleanup(func() { lchown = old })
	var got []string
	lchown = func(p string, _, _ int) error { got = append(got, filepath.Base(p)); return nil }
	t.Setenv("SUDO_UID", "501")
	t.Setenv("SUDO_GID", "20")
	data := []byte("-- dump\n")
	m := heldBackup("BK-20261014-002000-30D3", "shop", time.Date(2026, 10, 14, 0, 20, 0, 0, time.Local), data)

	dir := t.TempDir()
	if _, err := writeExport(m, bytes.NewReader(data), dir, false); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "shop") {
		t.Errorf("a new folder goes to the sudo user too: %v", got)
	}

	got = nil
	dir = t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := writeExport(m, bytes.NewReader(data), dir, false); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || slices.Contains(got, "shop") {
		t.Errorf("handed over %v, want only the data and its manifest", got)
	}
}
