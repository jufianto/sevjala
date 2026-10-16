package cmd

import (
	"cmp"
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

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
)

func newBackupCmd(g *globals) *cobra.Command {
	c := &cobra.Command{Use: "backup", Short: "Back up the database now (controller)"}
	var db string
	var tags []string
	now := &cobra.Command{
		Use:   "now",
		Short: "Back up the database now and send it to the backup PCs",
		Long: `Shows progress until every copy is verified. Tag it with a policy to keep it as long
as that policy keeps backups (default tag: manual). Ctrl-C stops watching; the backup
carries on.`,
		Example: `  sevjala backup now
  sevjala backup now --db shop
  sevjala backup now --tag weekly`,
		Args: cobra.NoArgs,
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
	now.Flags().StringVar(&db, "db", "", "back up only this database (`NAME`, one of those set with db set)")
	now.Flags().StringSliceVar(&tags, "tag", nil, "`POLICY` to keep it under, from 'sevjala policy list' (default: manual)")
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
	good, settled := true, true
	for i, c := range b.Copies {
		parts[i] = c.Hostname + " " + c.State
		if c.State != "verified" {
			good = false
			if c.LastError != "" {
				parts[i] += " (" + c.LastError + ")"
			}
		}
		// A copy that failed once is retried by the agent for as long as it takes;
		// watching it would never end while that PC is off.
		if c.State != "verified" && c.LastError == "" && !slices.Contains([]string{"failed", "offline", "corrupt", "deleted"}, c.State) {
			settled = false
		}
	}
	lines += "\n  " + strings.Join(parts, " · ")
	done = b.State == "distributed" || b.State == "partial" || settled
	if done && !good {
		lines += "\n  the backup is safe on this PC; missing copies are sent when those PCs answer again (see 'sevjala backups list')"
	}
	return lines, done, good
}

func newBackupsCmd(g *globals) *cobra.Command {
	c := &cobra.Command{Use: "backups", Short: "List backups, and copy one out into a folder"}
	var matrix, all bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List backups and where their copies are",
		Long: `On the controller: every backup and where its copies are. COPIES counts the backup PCs
holding a verified copy; --matrix shows one column per PC.
On a backup PC: the backups this PC holds.`,
		Example: `  sevjala backups list
  sevjala backups list --matrix
  sevjala backups list --all       (also expired backups)`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			st, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Status, error) {
				return cl.Status(ctx)
			})
			if err != nil {
				return err
			}
			if st.Role == config.RoleNode {
				held, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) ([]inventory.Manifest, error) {
					return cl.Held(ctx)
				})
				if err != nil {
					return err
				}
				renderHeld(c.OutOrStdout(), held)
				return nil
			}
			bs, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) ([]localapi.Backup, error) {
				return cl.Backups(ctx, all)
			})
			if err != nil {
				return err
			}
			if matrix {
				renderMatrix(c.OutOrStdout(), bs, runtime.GOOS)
				return nil
			}
			ps, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) ([]localapi.Policy, error) {
				return cl.Policies(ctx)
			})
			if err != nil {
				return err
			}
			order := make([]string, len(ps))
			for i, p := range ps {
				order[i] = p.Name
			}
			renderBackups(c.OutOrStdout(), bs, order)
			return nil
		},
	}
	list.Flags().BoolVar(&matrix, "matrix", false, "one column per node")
	list.Flags().BoolVar(&all, "all", false, "include expired backups (no copy left anywhere)")
	c.AddCommand(list, newBackupsExportCmd(g))
	return c
}

// stateMeaning explains the STATE column under the list.
var stateMeaning = []struct{ state, meaning string }{
	{"distributed", "on every backup PC it should be on"},
	{"created", "copies are still being sent"},
	{"partial", "some copies are missing or failed; retention sends them again"},
	{"dumping", "the database is being dumped"},
	{"failed", "the dump failed; sevjala doctor checks the database"},
	{"expired", "no copy is left anywhere"},
}

// renderBackups lists tags in the order of policies (policy list order),
// other tags after them.
func renderBackups(w io.Writer, bs []localapi.Backup, policies []string) {
	if len(bs) == 0 {
		fmt.Fprintln(w, "no backups yet; run 'sevjala backup now'")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "BACKUP\tDATABASE\tCREATED\tSIZE\tTAGS\tSTATE\tCOPIES")
	shown := map[string]bool{}
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
			strings.Join(byPolicy(b.Tags, policies), ","), b.State, verified, live)
		shown[b.State] = true
	}
	tw.Flush()
	var legend []string
	for _, s := range stateMeaning {
		if shown[s.state] {
			legend = append(legend, s.state+" = "+s.meaning)
		}
	}
	if len(legend) > 0 {
		fmt.Fprintf(w, "\n%s\n", strings.Join(legend, "\n"))
	}
}

func byPolicy(tags, policies []string) []string {
	out := slices.Clone(tags)
	rank := func(t string) int {
		if i := slices.Index(policies, t); i >= 0 {
			return i
		}
		return len(policies)
	}
	slices.SortStableFunc(out, func(a, b string) int { return cmp.Or(rank(a)-rank(b), strings.Compare(a, b)) })
	return out
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
