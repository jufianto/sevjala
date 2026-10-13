package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jufianto/sevjala/localapi"
)

func parseSet(t *testing.T, args []string, stdin string) (localapi.DBSettings, error) {
	t.Helper()
	c, f := dbSetCmd(&globals{})
	if err := c.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return f.settings(c.Flags(), strings.NewReader(stdin), func() (string, error) { return "prompted", nil })
}

func TestDBSetFlags(t *testing.T) {
	s, err := parseSet(t, []string{"--port", "3307", "--db", "shop,legacy", "--password"}, "")
	if err != nil || *s.Port != 3307 || s.Host != nil || *s.Password != "prompted" || len(s.Databases) != 2 {
		t.Errorf("%+v, %v", s, err)
	}
	s, err = parseSet(t, []string{"--password-stdin"}, "p a$s\r\n")
	if err != nil || *s.Password != "p a$s" || s.Databases != nil {
		t.Errorf("%+v, %v", s, err)
	}
	s, err = parseSet(t, []string{"--no-password"}, "")
	if err != nil || s.Password == nil || *s.Password != "" {
		t.Errorf("%+v, %v", s, err)
	}
	for _, args := range [][]string{{"--password", "--no-password"}, {}, {"--password-stdin"}} {
		var ee *exitError
		if _, err := parseSet(t, args, ""); !errors.As(err, &ee) || ee.code != 2 {
			t.Errorf("%v: err = %v", args, err)
		}
	}
}

func TestRenderDetection(t *testing.T) {
	var b bytes.Buffer
	renderDetection(&b, localapi.Detection{Dumper: `C:\xampp\mysql\bin\mysqldump.exe`, Client: `C:\xampp\mysql\bin\mysql.exe`,
		ServerVersion: "10.4.32-MariaDB", Flavour: "MariaDB", Databases: []localapi.DBDatabase{
			{Name: "legacy", Tables: 2, NonInnoDB: []string{"posts (MyISAM)"}},
			{Name: "shop", Tables: 12, Configured: true}}})
	out := b.String()
	for _, want := range []string{"mysqldump.exe", "MariaDB 10.4.32-MariaDB", "lock-tables (not InnoDB: posts (MyISAM))", "single-transaction", "yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "db set --db") {
		t.Errorf("hint shown although shop is configured:\n%s", out)
	}
}
