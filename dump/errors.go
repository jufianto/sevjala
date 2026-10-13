package dump

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// plainError carries a message an office operator can act on; the failing
// command's own error stays reachable for errors.Is/As.
type plainError struct {
	msg string
	err error
}

func (e *plainError) Error() string { return e.msg }
func (e *plainError) Unwrap() error { return e.err }

var (
	codeLine    = regexp.MustCompile(`(?:ERROR |Got error: |Error )(\d{4})(?: \([0-9A-Z]{5}\))?:\s*(.*)`)
	codeTrailer = regexp.MustCompile(`^(.*)\((\d{4})\)\s*$`)
	userInText  = regexp.MustCompile(`user '([^']*)'@`)
	dbInText    = regexp.MustCompile(`(?:to database|Unknown database) '([^']*)'`)
)

// explain turns a failed mysql/mysqldump run (its stderr and the exec or
// write error) into a plain message. The password is scrubbed from stderr and
// never part of the message.
func explain(tool string, conn Conn, db, stderr string, err error) error {
	return &plainError{msg: plainMessage(tool, conn, db, conn.scrub(stderr), err), err: err}
}

func plainMessage(tool string, conn Conn, db, stderr string, err error) string {
	if diskFull(err) {
		return "not enough disk space to write the dump"
	}
	code, text := mysqlCode(stderr)
	user := conn.User
	if m := userInText.FindStringSubmatch(text); m != nil {
		user = m[1]
	}
	if m := dbInText.FindStringSubmatch(text); m != nil {
		db = m[1]
	}
	switch {
	case code == 2002 || code == 2003 || code == 2005 ||
		code == 2013 && strings.Contains(text, "initial communication packet"):
		addr := ""
		if conn.Host != "" {
			addr = fmt.Sprintf(" at %s:%d", conn.Host, conn.Port)
		}
		return "MySQL is not running or not reachable" + addr + ". Start MySQL (XAMPP Control Panel → Start), then try again."
	case code == 1045:
		if user != "" {
			user = " for user '" + user + "'"
		}
		return "MySQL refused the password" + user + ". Save the right one with: sevjala db set --password"
	case code == 1049:
		return fmt.Sprintf("database '%s' does not exist on this MySQL server", db)
	case code == 1044 || code == 1142 || code == 1227:
		who, what := "MySQL user", "the requested data"
		if user != "" {
			who = "MySQL user '" + user + "'"
		}
		if db != "" {
			what = "'" + db + "'"
		}
		return who + " is not allowed to read " + what
	case code != 0:
		return fmt.Sprintf("MySQL error %d: %s", code, text)
	}
	last := lastLine(stderr)
	var ee *exec.ExitError
	switch {
	case last != "":
	case err == nil || errors.As(err, &ee):
		return tool + " stopped unexpectedly"
	default:
		last = innermost(err).Error()
	}
	return tool + " failed: " + last
}

// mysqlCode finds the first MySQL error code in the client's or mysqldump's
// stderr and the text that goes with it.
func mysqlCode(stderr string) (int, string) {
	var trailer string
	var trailerCode int
	for line := range strings.Lines(stderr) {
		line = strings.TrimSpace(line)
		if m := codeLine.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n, strings.TrimSpace(m[2])
		}
		if m := codeTrailer.FindStringSubmatch(line); m != nil && trailerCode == 0 {
			trailerCode, _ = strconv.Atoi(m[2])
			trailer = strings.TrimSpace(m[1])
		}
	}
	return trailerCode, trailer
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func innermost(err error) error {
	for {
		u := errors.Unwrap(err)
		if u == nil {
			return err
		}
		err = u
	}
}

func diskFull(err error) bool {
	return err != nil && (errors.Is(err, syscall.ENOSPC) || osDiskFull(err))
}
