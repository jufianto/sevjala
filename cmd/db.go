package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/jufianto/sevjala/localapi"
)

func newDBCmd(g *globals) *cobra.Command {
	c := &cobra.Command{Use: "db", Short: "Find and choose the database to back up (controller)"}
	c.AddCommand(&cobra.Command{
		Use:   "detect",
		Short: "Find MySQL tools and list the databases they can reach",
		Long: `Looks for mysqldump (XAMPP first, then PATH), connects with the saved settings, and
lists the databases with their size and whether they are backed up.`,
		Example: `  sevjala db detect`,
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			d, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Detection, error) {
				return cl.DetectDatabase(ctx)
			})
			if err != nil {
				return err
			}
			renderDetection(c.OutOrStdout(), d)
			return nil
		},
	}, newDBSetCmd(g))
	return c
}

type dbFlags struct {
	host, user, dumper                  string
	port                                int
	dbs                                 []string
	password, passwordStdin, noPassword bool
}

func newDBSetCmd(g *globals) *cobra.Command { c, _ := dbSetCmd(g); return c }

// dbSetCmd also returns the flags it fills, so tests can parse without running.
func dbSetCmd(g *globals) (*cobra.Command, *dbFlags) {
	f := &dbFlags{}
	c := &cobra.Command{
		Use:   "set",
		Short: "Choose the databases to back up and how to connect; tested before it is saved",
		Long: `Only the flags you give change. The connection is tested first; a wrong password or
unknown database is refused and the old settings stay. The password is kept in the
system keyring, never in a file.`,
		Example: `  sevjala db set --db shop --no-password          (XAMPP default: root without password)
  sevjala db set --db shop,crm --password         (asks for the password, not shown)
  sevjala db set --user backup --password
  sevjala db set --host 127.0.0.1 --port 3307`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			s, err := f.settings(c.Flags(), c.InOrStdin(), promptPassword(c.ErrOrStderr()))
			if err != nil {
				return err
			}
			d, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Detection, error) {
				return cl.SetDatabase(ctx, s)
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), "saved")
			renderDetection(c.OutOrStdout(), d)
			return nil
		},
	}
	fl := c.Flags()
	fl.StringVar(&f.host, "host", "", "database host")
	fl.IntVar(&f.port, "port", 0, "database port")
	fl.StringVar(&f.user, "user", "", "database user")
	fl.StringVar(&f.dumper, "dumper", "", "path to mysqldump (default: XAMPP, then PATH)")
	fl.StringSliceVar(&f.dbs, "db", nil, "databases to back up, comma separated")
	fl.BoolVar(&f.password, "password", false, "prompt for the password")
	fl.BoolVar(&f.passwordStdin, "password-stdin", false, "read the password from the first line of stdin")
	fl.BoolVar(&f.noPassword, "no-password", false, "connect without a password")
	return c, f
}

// settings turns the flags given into a partial update; flags left out keep
// the agent's current value.
func (f *dbFlags) settings(fs *pflag.FlagSet, stdin io.Reader, prompt func() (string, error)) (localapi.DBSettings, error) {
	var s localapi.DBSettings
	n := 0
	for _, b := range []bool{f.password, f.passwordStdin, f.noPassword} {
		if b {
			n++
		}
	}
	if n > 1 {
		return s, usagef("use only one of --password, --password-stdin and --no-password")
	}
	if fs.Changed("host") {
		s.Host = &f.host
	}
	if fs.Changed("port") {
		s.Port = &f.port
	}
	if fs.Changed("user") {
		s.User = &f.user
	}
	if fs.Changed("dumper") {
		s.Dumper = &f.dumper
	}
	if fs.Changed("db") {
		s.Databases = append([]string{}, f.dbs...)
	}
	switch {
	case f.noPassword:
		empty := ""
		s.Password = &empty
	case f.passwordStdin:
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return s, fail(err)
		}
		pw := strings.TrimRight(line, "\r\n")
		if pw == "" {
			return s, usagef("--password-stdin read an empty line; use --no-password for no password")
		}
		s.Password = &pw
	case f.password:
		pw, err := prompt()
		if err != nil {
			return s, fail(err)
		}
		s.Password = &pw
	}
	if s.Host == nil && s.Port == nil && s.User == nil && s.Dumper == nil && s.Databases == nil && s.Password == nil {
		return s, usagef("nothing to change; see 'sevjala db set --help'")
	}
	return s, nil
}

func promptPassword(w io.Writer) func() (string, error) {
	return func() (string, error) {
		fmt.Fprint(w, "database password: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(w)
		if err != nil {
			return "", fmt.Errorf("read the password (no terminal? use --password-stdin): %w", err)
		}
		return string(b), nil
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func renderDetection(w io.Writer, d localapi.Detection) {
	fmt.Fprintf(w, "dumper  %s\nclient  %s\nserver  %s %s\n\n", d.Dumper, d.Client, d.Flavour, d.ServerVersion)
	if len(d.Databases) == 0 {
		fmt.Fprintln(w, "no databases besides the system ones")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "DATABASE\tTABLES\tMODE\tCONFIGURED")
	configured := false
	for _, db := range d.Databases {
		mode := "single-transaction"
		if len(db.NonInnoDB) > 0 {
			mode = "lock-tables (not InnoDB: " + strings.Join(db.NonInnoDB, ", ") + ")"
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", db.Name, db.Tables, mode, yesNo(db.Configured))
		configured = configured || db.Configured
	}
	tw.Flush()
	if !configured {
		fmt.Fprintln(w, "\nchoose what to back up: sevjala db set --db <name>[,<name>]")
	}
}
