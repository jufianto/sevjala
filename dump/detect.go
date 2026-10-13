package dump

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type Conn struct {
	Host     string
	Port     int
	User     string
	Password string
}

func (c Conn) args() []string {
	return []string{"--host=" + c.Host, "--port=" + strconv.Itoa(c.Port), "--user=" + c.User}
}

// env hands the password over the only way that keeps it off the command
// line and off disk; an inherited MYSQL_PWD never leaks through.
func (c Conn) env() []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "MYSQL_PWD=") })
	if c.Password != "" {
		env = append(env, "MYSQL_PWD="+c.Password)
	}
	return env
}

func (c Conn) scrub(s string) string {
	if c.Password == "" {
		return s
	}
	return strings.ReplaceAll(s, c.Password, "***")
}

type Database struct {
	Name      string
	Tables    int
	NonInnoDB []string // "table (ENGINE)"
}

// Consistency is --single-transaction unless a table cannot take part in a
// transaction, which needs --lock-tables instead.
func (d Database) Consistency() string {
	if len(d.NonInnoDB) > 0 {
		return "--lock-tables"
	}
	return "--single-transaction"
}

type Detection struct {
	Tools         Tools
	ServerVersion string
	Flavour       string
	Databases     []Database
}

var systemDBs = []string{"information_schema", "performance_schema", "mysql", "sys"}

func Detect(ctx context.Context, tools Tools, conn Conn) (Detection, error) {
	d := Detection{Tools: tools, Flavour: "MySQL"}
	rows, err := query(ctx, tools, conn, "SELECT VERSION()")
	if err != nil {
		return Detection{}, err
	}
	if len(rows) == 0 {
		return Detection{}, errors.New("SELECT VERSION() returned nothing")
	}
	d.ServerVersion = rows[0][0]
	if strings.Contains(strings.ToLower(d.ServerVersion), "mariadb") {
		d.Flavour = "MariaDB"
	}
	if rows, err = query(ctx, tools, conn, "SHOW DATABASES"); err != nil {
		return Detection{}, err
	}
	for _, r := range rows {
		if !slices.Contains(systemDBs, r[0]) {
			d.Databases = append(d.Databases, Database{Name: r[0]})
		}
	}
	rows, err = query(ctx, tools, conn, "SELECT TABLE_SCHEMA, TABLE_NAME, IFNULL(ENGINE, '') FROM information_schema.TABLES "+
		"WHERE TABLE_TYPE = 'BASE TABLE' ORDER BY TABLE_SCHEMA, TABLE_NAME")
	if err != nil {
		return Detection{}, err
	}
	for _, r := range rows {
		i := slices.IndexFunc(d.Databases, func(db Database) bool { return db.Name == r[0] })
		if i < 0 || len(r) < 3 {
			continue
		}
		d.Databases[i].Tables++
		if !strings.EqualFold(r[2], "InnoDB") {
			d.Databases[i].NonInnoDB = append(d.Databases[i].NonInnoDB, fmt.Sprintf("%s (%s)", r[1], r[2]))
		}
	}
	return d, nil
}

func query(ctx context.Context, tools Tools, conn Conn, sql string) ([][]string, error) {
	args := append(conn.args(), "--batch", "--skip-column-names", "-e", sql)
	cmd := exec.CommandContext(ctx, tools.Client, args...)
	cmd.Env = conn.env()
	stderr := &capped{max: 64 << 10}
	cmd.Stderr = stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %v: %s", filepath.Base(tools.Client), err, strings.TrimSpace(conn.scrub(stderr.String())))
	}
	var rows [][]string
	for line := range strings.Lines(string(out)) {
		if line = strings.TrimRight(line, "\r\n"); line != "" {
			rows = append(rows, strings.Split(line, "\t"))
		}
	}
	return rows, nil
}

// capped keeps the first max bytes written to it.
type capped struct {
	buf bytes.Buffer
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (c *capped) String() string { return c.buf.String() }
