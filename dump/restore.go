package dump

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

var (
	namePattern    = regexp.MustCompile(`^[A-Za-z0-9_$][A-Za-z0-9_$-]{0,63}$`)
	charsetPattern = regexp.MustCompile(`^[A-Za-z0-9_]*$`)
)

// ValidName accepts database names restore can quote without escaping and
// that the mysql client cannot take for an option (no leading -).
func ValidName(name string) bool { return namePattern.MatchString(name) }

// DefaultTarget names a restore of db made at created, e.g.
// shop_restored_20261014_0030, trimming db so the name fits MySQL's 64.
func DefaultTarget(db string, created time.Time) string {
	suffix := "_restored_" + created.Local().Format("20060102_1504")
	return db[:min(len(db), 64-len(suffix))] + suffix
}

// Schema reports db's default character set and collation, and whether it exists.
func Schema(ctx context.Context, tools Tools, conn Conn, db string) (charset, collation string, exists bool, err error) {
	if !ValidName(db) {
		return "", "", false, fmt.Errorf("database name %q has characters Sevjala cannot restore safely", db)
	}
	rows, err := query(ctx, tools, conn, "SELECT DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME FROM information_schema.SCHEMATA "+
		"WHERE SCHEMA_NAME = '"+db+"'")
	if err != nil || len(rows) == 0 || len(rows[0]) < 2 {
		return "", "", false, err
	}
	return rows[0][0], rows[0][1], true, nil
}

// CreateDatabase creates name, with charset and collation when they are given.
func CreateDatabase(ctx context.Context, tools Tools, conn Conn, name, charset, collation string) error {
	if !ValidName(name) || !charsetPattern.MatchString(charset) || !charsetPattern.MatchString(collation) {
		return fmt.Errorf("cannot create database %q with character set %q", name, charset)
	}
	sql := "CREATE DATABASE `" + name + "`"
	if charset != "" {
		sql += " CHARACTER SET " + charset
	}
	if collation != "" {
		sql += " COLLATE " + collation
	}
	_, err := query(ctx, tools, conn, sql)
	return err
}

func DropDatabase(ctx context.Context, tools Tools, conn Conn, name string) error {
	if !ValidName(name) {
		return fmt.Errorf("cannot drop database %q", name)
	}
	_, err := query(ctx, tools, conn, "DROP DATABASE `"+name+"`")
	return err
}

// TableCount counts db's base tables.
func TableCount(ctx context.Context, tools Tools, conn Conn, db string) (int, error) {
	if !ValidName(db) {
		return 0, fmt.Errorf("cannot count tables of %q", db)
	}
	rows, err := query(ctx, tools, conn, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = '"+db+
		"' AND TABLE_TYPE = 'BASE TABLE'")
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return strconv.Atoi(rows[0][0])
}

// Load feeds a gzip'd dump into db through the mysql client.
func Load(ctx context.Context, tools Tools, conn Conn, db string, gz io.Reader) error {
	zr, err := gzip.NewReader(gz)
	if err != nil {
		return fmt.Errorf("the backup file is not a gzip file: %w", err)
	}
	cmd := exec.CommandContext(ctx, tools.Client, append(conn.args(), "--default-character-set=utf8mb4", db)...)
	cmd.Env = conn.env()
	cmd.Stdin = zr
	stderr := &capped{max: 64 << 10}
	cmd.Stderr = stderr
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		return explain("mysql", conn, db, stderr.String(), err)
	}
	return nil
}
