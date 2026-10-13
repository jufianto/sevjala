package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
)

func newBackupCmd(g *globals) *cobra.Command {
	c := &cobra.Command{Use: "backup", Short: "Run a backup (controller)"}
	var db string
	var tags []string
	now := &cobra.Command{
		Use:   "now",
		Short: "Dump the database now and push it to every paired node",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			jobs, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) ([]localapi.BackupJob, error) {
				return cl.BackupNow(ctx, db, tags)
			})
			if err != nil {
				return err
			}
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			ok, err := watchBackups(ctx, localapi.NewClient(localapi.Endpoint(dataDir)).Backup, jobs, c.OutOrStdout(), time.Second)
			switch {
			case ctx.Err() != nil:
				fmt.Fprintln(c.ErrOrStderr(), "still running in the agent; see 'sevjala backups list'")
				return &exitError{code: 130}
			case err != nil:
				return fail(agentProblem(err, nil))
			case !ok:
				return errSilent1
			}
			return nil
		},
	}
	now.Flags().StringVar(&db, "db", "", "back up only this configured database")
	now.Flags().StringSliceVar(&tags, "tag", nil, "tag the backup (default: manual)")
	c.AddCommand(now)
	return c
}

// watchBackups polls each backup until it settles and prints every change.
// ok is false when a dump failed or a copy did not verify.
func watchBackups(ctx context.Context, get func(context.Context, string) (localapi.Backup, error),
	jobs []localapi.BackupJob, w io.Writer, every time.Duration) (bool, error) {
	ok := true
	for _, j := range jobs {
		jok, err := watchBackup(ctx, get, j.BackupID, w, every)
		if err != nil || ctx.Err() != nil {
			return false, err
		}
		ok = ok && jok
	}
	return ok, nil
}

func watchBackup(ctx context.Context, get func(context.Context, string) (localapi.Backup, error), id string, w io.Writer, every time.Duration) (bool, error) {
	var last string
	var printed time.Time
	for {
		b, err := get(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return false, nil
			}
			return false, err
		}
		line, done, good := backupLines(b)
		throttled := b.State == localapi.BackupDumping && last != "" && time.Since(printed) < 5*time.Second
		if line != last && !throttled {
			fmt.Fprintln(w, line)
			last, printed = line, time.Now()
		}
		if done {
			return good, nil
		}
		select {
		case <-ctx.Done():
			return false, nil
		case <-time.After(every):
		}
	}
}

// backupLines renders one poll; done once nothing changes without an M3
// retry, good when every copy is verified.
func backupLines(b localapi.Backup) (lines string, done, good bool) {
	head := b.BackupID + " " + b.Database + ":"
	switch b.State {
	case localapi.BackupDumping:
		return fmt.Sprintf("%s dumping… %s", head, inventory.HumanBytes(b.Bytes)), false, false
	case localapi.BackupFailed:
		return fmt.Sprintf("%s dump failed: %s", head, b.Error), true, false
	}
	lines = fmt.Sprintf("%s %s %s sha256:%s", head, b.State, inventory.HumanBytes(b.Size), b.SHA256[:min(12, len(b.SHA256))])
	if len(b.Copies) == 0 {
		return lines + "\n  no paired nodes: the backup is only in staging on this PC", true, true
	}
	parts := make([]string, len(b.Copies))
	good = true
	for i, c := range b.Copies {
		parts[i] = c.Hostname + " " + c.State
		if c.State != "verified" {
			good = false
			if c.LastError != "" {
				parts[i] += " (" + c.LastError + ")"
			}
		}
	}
	return lines + "\n  " + strings.Join(parts, " · "), b.State == "distributed" || b.State == "partial", good
}

func newBackupsCmd(g *globals) *cobra.Command {
	c := &cobra.Command{Use: "backups", Short: "Backups the controller knows about"}
	var matrix, all bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List backups and where their copies are",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			bs, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) ([]localapi.Backup, error) {
				return cl.Backups(ctx, all)
			})
			if err != nil {
				return err
			}
			if matrix {
				renderMatrix(c.OutOrStdout(), bs, runtime.GOOS)
			} else {
				renderBackups(c.OutOrStdout(), bs)
			}
			return nil
		},
	}
	list.Flags().BoolVar(&matrix, "matrix", false, "one column per node")
	list.Flags().BoolVar(&all, "all", false, "include expired backups (no copy left anywhere)")
	c.AddCommand(list)
	return c
}

func renderBackups(w io.Writer, bs []localapi.Backup) {
	if len(bs) == 0 {
		fmt.Fprintln(w, "no backups yet; run 'sevjala backup now'")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "BACKUP\tDATABASE\tCREATED\tSIZE\tTAGS\tSTATE\tCOPIES")
	for _, b := range bs {
		created := "-"
		if !b.CreatedAt.IsZero() {
			created = b.CreatedAt.Local().Format("2006-01-02 15:04")
		}
		verified, live := 0, 0
		for _, c := range b.Copies {
			switch c.State {
			case "deleted":
				continue
			case "verified":
				verified++
			}
			live++
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d/%d verified\n", b.BackupID, b.Database, created, inventory.HumanBytes(b.Size),
			strings.Join(b.Tags, ","), b.State, verified, live)
	}
	tw.Flush()
}

// renderMatrix uses ASCII marks on Windows, whose console code pages lack ✓.
func renderMatrix(w io.Writer, bs []localapi.Backup, goos string) {
	marks := map[string]string{"verified": "✓", "pending": "…", "transferring": "…", "failed": "✗", "offline": "○",
		"corrupt": "!", "deleted": "-"}
	if goos == "windows" {
		marks = map[string]string{"verified": "v", "pending": ".", "transferring": ".", "failed": "x", "offline": "o",
			"corrupt": "!", "deleted": "-"}
	}
	var hosts []string
	for _, b := range bs {
		for _, c := range b.Copies {
			if !slices.Contains(hosts, c.Hostname) {
				hosts = append(hosts, c.Hostname)
			}
		}
	}
	slices.Sort(hosts)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "BACKUP\tDATABASE\t"+strings.Join(hosts, "\t"))
	for _, b := range bs {
		cells := make([]string, len(hosts))
		for i, h := range hosts {
			cells[i] = "-"
			for _, c := range b.Copies {
				if c.Hostname == h {
					cells[i] = marks[c.State]
				}
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", b.BackupID, b.Database, strings.Join(cells, "\t"))
	}
	tw.Flush()
	fmt.Fprintf(w, "\n%s verified  %s pending  %s failed  %s offline  ! corrupt  - no copy (deleted or not planned)\n",
		marks["verified"], marks["pending"], marks["failed"], marks["offline"])
}
