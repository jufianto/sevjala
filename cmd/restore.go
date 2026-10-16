package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
)

func newRestoreCmd(g *globals) *cobra.Command {
	var req localapi.RestoreRequest
	var status bool
	c := &cobra.Command{
		Use:   "restore <backup|latest>",
		Short: "Restore a backup into a new database, or replace the live one (controller)",
		Long: `Restores a backup made by Sevjala. By default it goes into a NEW database named
<database>_restored_<date>, so the live database is never touched: check the restored
copy, then point your application at it or copy what you need from it.

--replace-production replaces the live database instead. Sevjala first backs it up (an
ordinary backup tagged manual, so you can undo), then drops it and loads the backup. You
type the database name to confirm. The database is unavailable until loading ends.

The backup is read from the controller's own copy, or fetched from a backup PC when this
PC no longer has it; its sha256 is checked before anything is loaded. --file restores a
file copied out with 'sevjala backups export', e.g. on a new controller after the old
one died.

Ctrl-C stops watching; the restore carries on. 'sevjala restore --status' shows it again.`,
		Example: `  sevjala restore latest
  sevjala restore BK-20261016-101500-A8F2 --into shop_check
  sevjala restore latest --db shop --replace-production
  sevjala restore --file D:\Sevjala-export\shop\2026-10-16_10-15-00.sql.gz
  sevjala restore --status`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			switch {
			case status && (len(args) > 0 || req.File != "" || req.Into != "" || req.Replace):
				return usagef("--status takes no backup and no other flag")
			case status:
				return showRestore(g, c)
			case req.File != "" && len(args) == 1:
				return usagef("give a backup or --file, not both")
			case req.File == "" && len(args) == 0:
				return usagef("missing <backup|latest>")
			}
			if len(args) == 1 {
				req.Backup = args[0]
			}
			if req.File != "" {
				abs, err := filepath.Abs(req.File)
				if err != nil {
					return fail(err)
				}
				req.File = abs
			}
			if req.Replace {
				check := req
				check.DryRun = true
				plan, err := callAgent(g, 30*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Restore, error) {
					return cl.Restore(ctx, check)
				})
				if err != nil {
					return err
				}
				if req.Confirm = confirmReplace(c.InOrStdin(), c.OutOrStdout(), plan); req.Confirm != plan.Target {
					fmt.Fprintln(c.OutOrStdout(), "Cancelled: nothing was changed.")
					return errSilent1
				}
			}
			r, err := callAgent(g, 30*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Restore, error) {
				return cl.Restore(ctx, req)
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "Restoring %s (%s) into %s\n", r.BackupID, r.Database, r.Target)
			return followRestore(g, c)
		},
	}
	c.Flags().StringVar(&req.DB, "db", "", "with latest: restore the newest backup of database `NAME`")
	c.Flags().StringVar(&req.Into, "into", "", "restore into this new database `NAME` (default: <database>_restored_<date>)")
	c.Flags().BoolVar(&req.Replace, "replace-production", false, "replace the live database, after backing it up and a typed confirmation")
	c.Flags().StringVar(&req.File, "file", "", "restore the exported file at `PATH` (a .sql.gz with its .json beside it)")
	c.Flags().BoolVar(&status, "status", false, "show the running or last restore")
	c.MarkFlagsMutuallyExclusive("into", "replace-production")
	return c
}

func showRestore(g *globals, c *cobra.Command) error {
	if _, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Restore, error) {
		return cl.RestoreStatus(ctx)
	}); err != nil {
		return err
	}
	return followRestore(g, c)
}

func followRestore(g *globals, c *cobra.Command) error {
	dataDir, err := g.dataDir()
	if err != nil {
		return fail(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ok, err := watchRestore(ctx, localapi.NewClient(localapi.Endpoint(dataDir)).RestoreStatus, c.OutOrStdout(), time.Second)
	switch {
	case ctx.Err() != nil:
		fmt.Fprintln(c.ErrOrStderr(), "still running in the agent; see 'sevjala restore --status'")
		return &exitError{code: 130}
	case err != nil:
		return fail(agentProblem(err, nil))
	case !ok:
		return errSilent1
	}
	return nil
}

// confirmReplace says what --replace-production will do and returns what the
// operator typed; only the database name goes ahead.
func confirmReplace(in io.Reader, out io.Writer, p localapi.Restore) string {
	fmt.Fprintf(out, `This replaces the live database %s with backup %s
(made %s, %s compressed).
First Sevjala backs %s up as it is now, so this can be undone.
%s is unavailable from the moment it is dropped until loading ends; for a large
database that can take several minutes.
Type the database name (%s) to continue, anything else cancels: `,
		p.Target, p.BackupID, p.BackupCreatedAt.Local().Format("2006-01-02 15:04"), inventory.HumanBytes(p.Size),
		p.Target, p.Target, p.Target)
	line, _ := bufio.NewReader(in).ReadString('\n')
	return strings.TrimSpace(line)
}

// watchRestore prints each step, a percentage at most every 5 s, and the
// result; ok is false when the restore failed.
func watchRestore(ctx context.Context, get func(context.Context) (localapi.Restore, error), w io.Writer, every time.Duration) (bool, error) {
	var step, last string
	var printed time.Time
	for {
		r, err := get(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return false, nil
			}
			return false, err
		}
		if r.State != localapi.RestoreRunning {
			return restoreResult(w, r), nil
		}
		line := "  " + r.Step + "…"
		if r.Size > 0 && r.Bytes > 0 {
			line += fmt.Sprintf(" %d%% (%s of %s)", r.Bytes*100/r.Size, inventory.HumanBytes(r.Bytes), inventory.HumanBytes(r.Size))
		}
		if r.Step != step || (line != last && time.Since(printed) >= 5*time.Second) {
			fmt.Fprintln(w, line)
			step, last, printed = r.Step, line, time.Now()
		}
		select {
		case <-ctx.Done():
			return false, nil
		case <-time.After(every):
		}
	}
}

func restoreResult(w io.Writer, r localapi.Restore) bool {
	took := r.FinishedAt.Sub(r.StartedAt).Round(time.Second)
	switch {
	case r.State == localapi.RestoreFailed:
		fmt.Fprintf(w, "Restore failed: %s\n", r.Error)
		return false
	case r.Replace:
		fmt.Fprintf(w, "Replaced %s with %s: %d tables, in %s, from %s.\n", r.Target, r.BackupID, r.Tables, took, r.Source)
		fmt.Fprintf(w, "The backup made just before is %s; to undo: sevjala restore %s --replace-production\n", r.PreRestoreID, r.PreRestoreID)
	default:
		fmt.Fprintf(w, "Restored %s into %s: %d tables, in %s, from %s.\n", r.BackupID, r.Target, r.Tables, took, r.Source)
		fmt.Fprintf(w, "The live database %s was not touched.\n", r.Database)
	}
	return true
}
