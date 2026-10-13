package dump

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/dump/dumptest"
	"github.com/jufianto/sevjala/inventory"
)

func TestMain(m *testing.M) {
	dumptest.Main()
	os.Exit(m.Run())
}

const pw = `p a$s"wörd`

var conn = Conn{Host: "127.0.0.1", Port: 3306, User: "root", Password: pw}

func fakeTools(t *testing.T, mode string) (Tools, string) {
	bin, log := dumptest.Use(t, mode)
	return Tools{Dumper: bin, Client: bin}, log
}

func calls(t *testing.T, log string) []string {
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func TestFindTools(t *testing.T) {
	join := filepath.Join
	tests := []struct {
		name, goos, configured string
		files                  []string
		path                   map[string]string
		want                   Tools
		wantErr                string
	}{
		{name: "configured", goos: "linux", configured: "/opt/m/bin/mysqldump",
			files: []string{"/opt/m/bin/mysqldump", join("/opt/m/bin", "mysql")},
			want:  Tools{"/opt/m/bin/mysqldump", join("/opt/m/bin", "mysql")}},
		{name: "configured missing", goos: "linux", configured: "/nope/mysqldump", wantErr: "--dumper"},
		{name: "xampp windows", goos: "windows",
			files: []string{join(`C:\xampp\mysql\bin`, "mysqldump.exe"), join(`C:\xampp\mysql\bin`, "mysql.exe")},
			want:  Tools{join(`C:\xampp\mysql\bin`, "mysqldump.exe"), join(`C:\xampp\mysql\bin`, "mysql.exe")}},
		{name: "path", goos: "linux", path: map[string]string{"mysqldump": "/usr/bin/mysqldump"},
			files: []string{join("/usr/bin", "mysql")}, want: Tools{"/usr/bin/mysqldump", join("/usr/bin", "mysql")}},
		{name: "mariadb names", goos: "linux", path: map[string]string{"mariadb-dump": "/usr/bin/mariadb-dump"},
			files: []string{join("/usr/bin", "mariadb")}, want: Tools{"/usr/bin/mariadb-dump", join("/usr/bin", "mariadb")}},
		{name: "none", goos: "darwin", wantErr: "mysqldump not found"},
		{name: "no client", goos: "linux", path: map[string]string{"mysqldump": "/usr/bin/mysqldump"}, wantErr: "client not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookPath := func(n string) (string, error) {
				if p, ok := tt.path[n]; ok {
					return p, nil
				}
				return "", errors.New("not found")
			}
			exists := func(p string) bool { return slices.Contains(tt.files, p) }
			got, err := findTools(tt.configured, tt.goos, lookPath, exists)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestDetect(t *testing.T) {
	tools, _ := fakeTools(t, dumptest.OK)
	d, err := Detect(context.Background(), tools, conn)
	if err != nil {
		t.Fatal(err)
	}
	if d.ServerVersion != "10.4.32-MariaDB" || d.Flavour != "MariaDB" || d.Tools != tools {
		t.Errorf("detection %+v", d)
	}
	want := []Database{{Name: "legacy", Tables: 2, NonInnoDB: []string{"posts (MyISAM)"}}, {Name: "shop", Tables: 2}}
	if !reflect.DeepEqual(d.Databases, want) {
		t.Errorf("databases %+v, want %+v", d.Databases, want)
	}
	if d.Databases[0].Consistency() != "--lock-tables" || d.Databases[1].Consistency() != "--single-transaction" {
		t.Error("consistency flags")
	}
}

func TestPasswordOnlyInEnvAndScrubbed(t *testing.T) {
	tools, log := fakeTools(t, dumptest.OK)
	if _, err := Detect(context.Background(), tools, conn); err != nil {
		t.Fatal(err)
	}
	for _, line := range calls(t, log) {
		args, env, _ := strings.Cut(line, " MYSQL_PWD=")
		if strings.Contains(args, "wörd") || env != pw {
			t.Errorf("call %q", line)
		}
	}
	tools, _ = fakeTools(t, dumptest.Denied)
	_, err := Detect(context.Background(), tools, conn)
	if err == nil || !strings.Contains(err.Error(), "refused the password") || strings.Contains(err.Error(), "wörd") {
		t.Errorf("err = %v", err)
	}
}

func TestInheritedPasswordDropped(t *testing.T) {
	t.Setenv("MYSQL_PWD", "leak")
	tools, log := fakeTools(t, dumptest.OK)
	if _, err := Detect(context.Background(), tools, Conn{Host: "127.0.0.1", Port: 3306, User: "root"}); err != nil {
		t.Fatal(err)
	}
	for _, line := range calls(t, log) {
		if !strings.HasSuffix(line, " MYSQL_PWD=") {
			t.Errorf("call %q", line)
		}
	}
}

func opts(t *testing.T, mode, db string) (Options, string) {
	tools, log := fakeTools(t, mode)
	return Options{ID: "BK-20261013-101500-A8F2", Database: db, StagingDir: t.TempDir(), SourceAgent: "c960acf5",
		Created: time.Date(2026, 10, 13, 10, 15, 0, 0, time.UTC), Tags: []string{"manual"}, Tools: tools, Conn: conn}, log
}

func TestRun(t *testing.T) {
	o, log := opts(t, dumptest.OK, "shop")
	var progress int64
	o.Progress = func(n int64) { progress = n }
	m, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(o.StagingDir, o.ID+inventory.DataExt))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if m.SHA256 != hex.EncodeToString(sum[:]) || m.Size != int64(len(raw)) || progress != m.Size {
		t.Errorf("manifest %+v, file %d bytes, progress %d", m, len(raw), progress)
	}
	zr, _ := gzip.NewReader(strings.NewReader(string(raw)))
	if plain, _ := io.ReadAll(zr); string(plain) != dumptest.Dump("shop") {
		t.Error("gunzipped content differs from the dump")
	}
	if !slices.Contains(m.DumpFlags, "--single-transaction") || !slices.Contains(m.DumpFlags, "--default-character-set=utf8mb4") ||
		m.ServerVersion != "10.4.32-MariaDB" || m.BackupID != o.ID || m.Database != "shop" || m.V != 1 {
		t.Errorf("manifest %+v", m)
	}
	onDisk, err := inventory.ReadManifest(filepath.Join(o.StagingDir, o.ID+inventory.ManifestExt))
	if err != nil || onDisk.SHA256 != m.SHA256 || !onDisk.CreatedAt.Equal(m.CreatedAt) {
		t.Errorf("manifest on disk %+v, %v", onDisk, err)
	}
	if _, err := os.Stat(filepath.Join(o.StagingDir, o.ID+inventory.PartExt)); !errors.Is(err, os.ErrNotExist) {
		t.Error(".part left")
	}
	all := calls(t, log)
	if dumpCall := all[len(all)-1]; !strings.Contains(dumpCall, "--single-transaction shop MYSQL_PWD=") {
		t.Errorf("dump call %q", dumpCall)
	}
}

func TestRunLockTables(t *testing.T) {
	o, _ := opts(t, dumptest.OK, "legacy")
	m, err := Run(context.Background(), o)
	if err != nil || !slices.Contains(m.DumpFlags, "--lock-tables") {
		t.Errorf("%+v, %v", m, err)
	}
}

func TestRunFailuresLeaveNothing(t *testing.T) {
	tests := []struct{ name, mode, db, want string }{
		{"unknown database", dumptest.OK, "nope", "does not exist"},
		{"dumper exits non-zero", dumptest.DumpFails, "shop", "database 'shop' does not exist"},
		{"error on stderr", dumptest.DumpWarnsError, "shop", "Lost connection"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, _ := opts(t, tt.mode, tt.db)
			if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			if es, _ := os.ReadDir(o.StagingDir); len(es) != 0 {
				t.Errorf("staging not empty: %v", es)
			}
		})
	}
}

func TestRunCancelKillsDumper(t *testing.T) {
	o, _ := opts(t, dumptest.Hang, "shop")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := Run(ctx, o); err == nil {
		t.Fatal("cancelled dump succeeded")
	}
	if time.Since(start) > 10*time.Second {
		t.Error("dumper not killed")
	}
	if es, _ := os.ReadDir(o.StagingDir); len(es) != 0 {
		t.Errorf("staging not empty: %v", es)
	}
}

func TestCleanStaging(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "BK-20261013-101500-A8F2.part"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "BK-20261013-101500-A8F1.sql.gz"), []byte("x"), 0o600)
	removed, err := CleanStaging(dir)
	if err != nil || len(removed) != 1 || removed[0] != "BK-20261013-101500-A8F2.part" {
		t.Errorf("removed %v, %v", removed, err)
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Errorf("left %v", es)
	}
}

// Two backups drawing the same id must never swap contents: nodes would verify
// the wrong database under the first backup's id.
func TestRunNeverReplacesAStagedBackup(t *testing.T) {
	for _, ext := range []string{inventory.DataExt, inventory.ManifestExt} {
		t.Run(ext, func(t *testing.T) {
			o, _ := opts(t, dumptest.OK, "shop")
			existing := filepath.Join(o.StagingDir, o.ID+ext)
			if err := os.WriteFile(existing, []byte("first backup"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "already") {
				t.Fatalf("err = %v", err)
			}
			if got, _ := os.ReadFile(existing); string(got) != "first backup" {
				t.Errorf("staged file replaced: %q", got)
			}
			if es, _ := os.ReadDir(o.StagingDir); len(es) != 1 {
				t.Errorf("staging %v", es)
			}
		})
	}
}
