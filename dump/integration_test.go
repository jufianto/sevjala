//go:build integration

package dump

import (
	"bytes"
	"compress/gzip"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/inventory"
)

// Run by 'make test-integration' inside a mariadb:10.4 container.
func TestIntegrationDumpRestoreChecksum(t *testing.T) {
	addr := os.Getenv("SEVJALA_TEST_DB")
	if addr == "" {
		t.Skip("SEVJALA_TEST_DB not set; run 'make test-integration'")
	}
	host, portS, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portS)
	conn := Conn{Host: host, Port: port, User: "root"}
	tools, err := FindTools("")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// sql runs q[0] with -e, or pipes stdin into the database named by q.
	sql := func(stdin []byte, q ...string) string {
		t.Helper()
		args := append(conn.args(), "--batch", "--skip-column-names", "--default-character-set=utf8mb4")
		if stdin == nil {
			args = append(args, "-e", q[0])
		} else {
			args = append(args, q...)
		}
		cmd := exec.CommandContext(ctx, tools.Client, args...)
		cmd.Env = conn.env()
		cmd.Stdin = bytes.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", q, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	sql(nil, `DROP DATABASE IF EXISTS sj_shop; DROP DATABASE IF EXISTS sj_legacy;
		CREATE DATABASE sj_shop CHARACTER SET utf8mb4; CREATE DATABASE sj_legacy;
		CREATE TABLE sj_shop.orders (id INT PRIMARY KEY, note VARCHAR(64)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
		INSERT INTO sj_shop.orders WITH RECURSIVE s(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM s WHERE n < 5000)
			SELECT n, CONCAT('order ', n) FROM s;
		INSERT INTO sj_shop.orders VALUES (0, 'party 🎉');
		CREATE PROCEDURE sj_shop.hello() SELECT 1;
		CREATE TABLE sj_legacy.posts (id INT PRIMARY KEY, body TEXT) ENGINE=MyISAM;
		INSERT INTO sj_legacy.posts VALUES (1, 'a'), (2, 'b');`)

	d, err := Detect(ctx, tools, conn)
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]string{}
	for _, db := range d.Databases {
		modes[db.Name] = db.Consistency()
	}
	if modes["sj_shop"] != "--single-transaction" || modes["sj_legacy"] != "--lock-tables" || d.Flavour != "MariaDB" {
		t.Fatalf("detection %+v", d)
	}

	staging := t.TempDir()
	for _, db := range []struct{ name, table string }{{"sj_shop", "orders"}, {"sj_legacy", "posts"}} {
		m, err := Run(ctx, Options{ID: inventory.NewID(time.Now()), Database: db.name, StagingDir: staging,
			SourceAgent: "it", Created: time.Now(), Tools: tools, Conn: conn})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(filepath.Join(staging, m.BackupID+inventory.DataExt))
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		var plain bytes.Buffer
		if _, err := plain.ReadFrom(zr); err != nil {
			t.Fatal(err)
		}
		restored := db.name + "_restored"
		sql(nil, "DROP DATABASE IF EXISTS "+restored+"; CREATE DATABASE "+restored+" CHARACTER SET utf8mb4")
		sql(plain.Bytes(), restored)
		sums := strings.Split(sql(nil, "CHECKSUM TABLE "+db.name+"."+db.table+", "+restored+"."+db.table), "\n")
		if len(sums) != 2 || strings.Fields(sums[0])[1] != strings.Fields(sums[1])[1] {
			t.Errorf("%s checksums differ: %q", db.name, sums)
		}
	}
	if got := sql(nil, "SELECT note FROM sj_shop_restored.orders WHERE id = 0"); got != "party 🎉" {
		t.Errorf("emoji row = %q", got)
	}
	if got := sql(nil, "SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = 'sj_shop_restored'"); got != "1" {
		t.Errorf("routines restored = %s", got)
	}
}
