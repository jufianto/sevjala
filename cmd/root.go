package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"

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
		Use:           "sevjala",
		Short:         "Sevjala — Database Backup & Replication",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&g.dataDirFlag, "data-dir", "", "data directory (default: $SEVJALA_DATA_DIR, else the OS default)")
	root.AddCommand(newVersionCmd(), newAgentCmd(g), newServiceCmd(g), newDoctorCmd(g),
		newDiscoverCmd(g), newCodeCmd(g), newPairCmd(g), newAgentsCmd(g), newPingCmd(g),
		newDBCmd(g), newBackupCmd(g), newBackupsCmd(g), newPolicyCmd(g), newRetentionCmd(g), newSetupCmd(g))
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
	return exitCode(root.Execute(), errOut)
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
