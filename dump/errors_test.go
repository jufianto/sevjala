package dump

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

func TestExplain(t *testing.T) {
	c := Conn{Host: "127.0.0.1", Port: 3306, User: "root", Password: "s3cr3t"}
	const addr = "MySQL is not running or not reachable at 127.0.0.1:3306. Start MySQL (XAMPP Control Panel → Start), then try again."
	const pwMsg = "MySQL refused the password for user 'root'. Save the right one with: sevjala db set --password"
	tests := []struct {
		name, tool, db, stderr string
		err                    error
		conn                   Conn
		want                   string
	}{
		{name: "2003 windows", stderr: "ERROR 2003 (HY000): Can't connect to MySQL server on '127.0.0.1' (10061 \"Unknown error\")\r\n", want: addr},
		{name: "2003 linux", stderr: "ERROR 2003 (HY000): Can't connect to MySQL server on '127.0.0.1' (111)\n", want: addr},
		{name: "2003 mariadb 10.1", stderr: "ERROR 2003 (HY000): Can't connect to MySQL server on '127.0.0.1' (111 \"Connection refused\")", want: addr},
		{name: "2002 socket", stderr: "ERROR 2002 (HY000): Can't connect to local MySQL server through socket '/tmp/mysql.sock' (2)", want: addr},
		{name: "mysqldump 2003", tool: "mysqldump", stderr: "mysqldump: Got error: 2003: Can't connect to MySQL server on '127.0.0.1' (10061) when trying to connect", want: addr},
		{name: "2005", stderr: "ERROR 2005 (HY000): Unknown MySQL server host 'dbhost' (11001)", want: addr},
		{name: "2013 at connect", stderr: "ERROR 2013 (HY000): Lost connection to MySQL server at 'reading initial communication packet', system error: 0", want: addr},
		{name: "2013 mid dump", stderr: "mysqldump: Error 2013: Lost connection to server during query when dumping table `t` at row: 3", want: "MySQL error 2013: Lost connection to server during query when dumping table `t` at row: 3"},
		{name: "no address", conn: Conn{}, stderr: "ERROR 2003 (HY000): Can't connect", want: "MySQL is not running or not reachable. Start MySQL (XAMPP Control Panel → Start), then try again."},
		{name: "1045", stderr: "ERROR 1045 (28000): Access denied for user 'root'@'localhost' (using password: YES)", want: pwMsg},
		{name: "1045 other user", stderr: "ERROR 1045 (28000): Access denied for user 'bk'@'192.168.1.5' (using password: NO)", want: "MySQL refused the password for user 'bk'. Save the right one with: sevjala db set --password"},
		{name: "1045 no name", conn: Conn{Host: "h"}, stderr: "ERROR 1045 (28000): Access denied", want: "MySQL refused the password. Save the right one with: sevjala db set --password"},
		{name: "1049", db: "x", stderr: "ERROR 1049 (42000): Unknown database 'shop'", want: "database 'shop' does not exist on this MySQL server"},
		{name: "1049 mysqldump", tool: "mysqldump", stderr: "mysqldump: Got error: 1049: \"Unknown database 'shop'\" when selecting the database", want: "database 'shop' does not exist on this MySQL server"},
		{name: "1044", stderr: "ERROR 1044 (42000): Access denied for user 'bk'@'localhost' to database 'shop'", want: "MySQL user 'bk' is not allowed to read 'shop'"},
		{name: "1044 mysqldump trailer", db: "shop", stderr: "mysqldump: Couldn't execute 'show events': Access denied for user 'bk'@'localhost' to database 'shop' (1044)", want: "MySQL user 'bk' is not allowed to read 'shop'"},
		{name: "1142", db: "shop", stderr: "ERROR 1142 (42000): SELECT command denied to user 'bk'@'localhost' for table 'items'", want: "MySQL user 'bk' is not allowed to read 'shop'"},
		{name: "1227 fallback", db: "shop", stderr: "ERROR 1227 (42000): Access denied; you need (at least one of) the PROCESS privilege(s) for this operation", want: "MySQL user 'root' is not allowed to read 'shop'"},
		{name: "1227 generic", conn: Conn{}, stderr: "ERROR 1227 (42000): Access denied", want: "MySQL user is not allowed to read the requested data"},
		{name: "other code", stderr: "ERROR 1064 (42000): You have an error in your SQL syntax", want: "MySQL error 1064: You have an error in your SQL syntax"},
		{name: "no ERROR line", tool: "mysqldump", stderr: "warning\nmysqldump: Out of memory\n\n", err: errors.New("signal: killed"), want: "mysqldump failed: mysqldump: Out of memory"},
		{name: "silent exit", tool: "mysqldump", err: &exec.ExitError{}, want: "mysqldump stopped unexpectedly"},
		{name: "innermost cause", tool: "mysql", err: errors.Join(errors.New("outer")), want: "mysql failed: outer"},
		{name: "disk full unix", tool: "mysqldump", err: &wrapped{syscall.ENOSPC}, want: "not enough disk space to write the dump"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := c
			if tt.conn != (Conn{}) || tt.name == "no address" || tt.name == "1227 generic" {
				conn = tt.conn
			}
			err := explain(tt.tool, conn, tt.db, tt.stderr, tt.err)
			if err.Error() != tt.want {
				t.Errorf("got  %q\nwant %q", err, tt.want)
			}
			if tt.err != nil && !errors.Is(err, tt.err) {
				t.Error("original error not reachable")
			}
		})
	}
}

type wrapped struct{ error }

func (w *wrapped) Unwrap() error { return w.error }

func TestExplainNeverEchoesPassword(t *testing.T) {
	c := Conn{Host: "h", Port: 1, User: "u", Password: "s3cr3t"}
	for _, stderr := range []string{
		"ERROR 1064 (42000): near 's3cr3t'",
		"mysqldump: s3cr3t exploded",
	} {
		if err := explain("mysqldump", c, "d", stderr, nil); strings.Contains(err.Error(), "s3cr3t") {
			t.Errorf("%q", err)
		}
	}
}
