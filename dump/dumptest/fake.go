// Package dumptest runs a test binary as a fake mysqldump and mysql client,
// so tests need no database server: call Main first in TestMain, then Use.
package dumptest

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	envMode = "SEVJALA_FAKE_DB"
	envLog  = "SEVJALA_FAKE_DB_LOG"

	OK             = "ok"
	Denied         = "denied"
	DumpFails      = "dumpfail"
	DumpWarnsError = "dumperror"
	Hang           = "hang"
)

// Main turns the process into the fake when a test asked for one.
func Main() {
	if mode := os.Getenv(envMode); mode != "" {
		os.Exit(fake(mode, os.Args[1:]))
	}
}

// Use makes the test binary the fake for the rest of the test. Every
// invocation appends "<args> MYSQL_PWD=<value>" to log.
func Use(t testing.TB, mode string) (bin, log string) {
	t.Helper()
	log = filepath.Join(t.TempDir(), "calls.log")
	t.Setenv(envMode, mode)
	t.Setenv(envLog, log)
	return os.Args[0], log
}

// Dump is what the fake dumper writes for db.
func Dump(db string) string {
	return fmt.Sprintf("-- MariaDB dump 10.19\n-- Database: %s\nCREATE TABLE t (id int);\n", db) +
		strings.Repeat("INSERT INTO t VALUES (1);\n", 2000)
}

func fake(mode string, args []string) int {
	if p := os.Getenv(envLog); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintf(f, "%s MYSQL_PWD=%s\n", strings.Join(args, " "), os.Getenv("MYSQL_PWD"))
			f.Close()
		}
	}
	if mode == Denied {
		fmt.Fprintf(os.Stderr, "ERROR 1045 (28000): Access denied for user 'root'@'localhost' (password %s)\n", os.Getenv("MYSQL_PWD"))
		return 1
	}
	if i := slices.Index(args, "-e"); i >= 0 && i+1 < len(args) {
		return client(args[i+1])
	}
	return dumper(mode, args[len(args)-1])
}

func client(sql string) int {
	switch {
	case strings.Contains(sql, "VERSION()"):
		fmt.Println("10.4.32-MariaDB")
	case strings.HasPrefix(sql, "SHOW DATABASES"):
		fmt.Print("information_schema\nlegacy\nmysql\nperformance_schema\nshop\n")
	case strings.Contains(sql, "information_schema.TABLES"):
		fmt.Print("legacy\tposts\tMyISAM\nlegacy\tusers\tInnoDB\nshop\titems\tInnoDB\nshop\torders\tInnoDB\n")
	default:
		fmt.Fprintf(os.Stderr, "ERROR 1064: unexpected query %q\n", sql)
		return 1
	}
	return 0
}

func dumper(mode, db string) int {
	switch mode {
	case DumpFails:
		fmt.Fprintf(os.Stderr, "mysqldump: Got error: 1049: \"Unknown database '%s'\" when selecting the database\n", db)
		return 2
	case DumpWarnsError:
		fmt.Printf("-- partial dump of %s\n", db)
		fmt.Fprintln(os.Stderr, "mysqldump: Error 2013: Lost connection to server during query")
		return 0
	case Hang:
		fmt.Printf("-- dump of %s\n", db)
		time.Sleep(time.Hour)
		return 0
	}
	fmt.Print(Dump(db))
	return 0
}
