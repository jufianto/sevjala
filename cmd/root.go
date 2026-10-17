package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/config"
)

var version = "dev"

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit status %d", e.code)
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }

// errSilent1 exits 1 without a message; the command already printed why.
var errSilent1 = &exitError{code: 1}

func fail(err error) error { return &exitError{code: 1, err: err} }

func failf(format string, a ...any) error { return fail(fmt.Errorf(format, a...)) }

func usagef(format string, a ...any) error {
	return &exitError{code: 2, err: fmt.Errorf(format, a...)}
}

type globals struct {
	dataDirFlag string
}

func (g *globals) dataDir() (string, error) {
	return config.ResolveDataDir(g.dataDirFlag)
}

func newRootCmd() *cobra.Command {
	g := &globals{}
	root := &cobra.Command{
		Use:   "sevjala",
		Short: "Sevjala — Database Backup & Replication",
		Long: `Sevjala backs up a MySQL/MariaDB database (the controller PC) to backup PCs
on the same network (nodes). Most commands need no administrator rights; the few that
do ask for them. New here? Start with: sevjala setup`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().StringVar(&g.dataDirFlag, "data-dir", "", "`FOLDER` holding Sevjala's data (default: $SEVJALA_DATA_DIR, else the OS default)")
	root.PersistentFlags().Bool(elevatedMarker[2:], false, "")
	_ = root.PersistentFlags().MarkHidden(elevatedMarker[2:])
	root.AddCommand(newAgentCmd(g))
	for _, grp := range []struct {
		id, title string
		cmds      []*cobra.Command
	}{
		{"install", "Install & status:", []*cobra.Command{newStatusCmd(g), newUICmd(g), newTrayCmd(g), newSetupCmd(g), newUpdateCmd(g), newServiceCmd(g), newDoctorCmd(g), newVersionCmd()}},
		{"connect", "Connect PCs:", []*cobra.Command{newDiscoverCmd(g), newCodeCmd(g), newPairCmd(g), newAgentsCmd(g), newPingCmd(g)}},
		{"database", "Database:", []*cobra.Command{newDBCmd(g)}},
		{"backups", "Backups:", []*cobra.Command{newBackupCmd(g), newBackupsCmd(g), newRestoreCmd(g)}},
		{"schedule", "Schedules & cleanup:", []*cobra.Command{newPolicyCmd(g), newRetentionCmd(g)}},
	} {
		root.AddGroup(&cobra.Group{ID: grp.id, Title: grp.title})
		for _, c := range grp.cmds {
			c.GroupID = grp.id
			root.AddCommand(c)
		}
	}
	return root
}

// Execute runs the CLI. Started by a double-click on Windows, it runs setup and
// keeps the window open until Enter, so the operator can read the result.
func Execute() {
	args, own := os.Args[1:], ownConsole()
	if own && len(args) == 0 {
		args = []string{"setup"}
	}
	code := run(args, os.Stdout, os.Stderr)
	if own {
		fmt.Print("\nPress Enter to close ")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
	os.Exit(code)
}

func run(args []string, out, errOut io.Writer) int {
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(errOut)
	c, err := root.ExecuteC()
	var na *needsAdmin
	if errors.As(err, &na) {
		return rerunElevated(args, na, out, errOut)
	}
	code := exitCode(plainArgsError(c, err), errOut)
	if code == 2 && c != nil {
		fmt.Fprintf(errOut, "\n%s", c.UsageString())
	}
	return code
}

var (
	argCount    = regexp.MustCompile(`^accepts (\d+) arg\(s\), received (\d+)$`)
	placeholder = regexp.MustCompile(`<[^>]+>`)
)

// plainArgsError replaces cobra's "accepts 1 arg(s), received 0" with what
// is missing, named as in the command's usage line.
func plainArgsError(c *cobra.Command, err error) error {
	m := argCount.FindStringSubmatch(fmt.Sprint(err))
	if err == nil || c == nil || m == nil {
		return err
	}
	want, _ := strconv.Atoi(m[1])
	got, _ := strconv.Atoi(m[2])
	if p := placeholder.FindString(c.Use); got < want && p != "" {
		return fmt.Errorf("missing %s", p)
	}
	return fmt.Errorf("expected %d argument(s), got %d", want, got)
}

// exitCode maps an error to the process exit code. Errors our commands return
// are *exitError; anything else comes from cobra's parsing and is a usage error.
func exitCode(err error, errOut io.Writer) int {
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		if ee.err != nil {
			fmt.Fprintf(errOut, "sevjala: %v\n", ee.err)
		}
		return ee.code
	}
	fmt.Fprintf(errOut, "sevjala: %v\n", err)
	return 2
}
