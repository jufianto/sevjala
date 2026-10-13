package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	renderHeld(&b, nil)
	if b.String() != "this PC holds no backups yet\n" {
		t.Errorf("empty: %q", b.String())
	}
	b.Reset()
	renderHeld(&b, []inventory.Manifest{heldBackup("BK-20261014-002000-30D3", "shop",
		time.Date(2026, 10, 14, 0, 20, 0, 0, time.Local), make([]byte, 2048))})
	want := "BACKUP                   DATABASE  CREATED           SIZE     TAGS\n" +
		"BK-20261014-002000-30D3  shop      2026-10-14 00:20  2.0 KiB  daily\n" +
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
