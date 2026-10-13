package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/scheduler"
)

func newPolicyCmd(g *globals) *cobra.Command {
	c := &cobra.Command{Use: "policy", Short: "Backup schedules and how many backups each PC keeps (controller)", Long: `A policy is a schedule plus a keep count. Every backup is tagged with the policies that
were due when it was made; a backup PC keeps, per policy, that many of the newest backups
with its tag. A backup stays while any of its tags still keeps it.

Schedules use cron: minute hour day month weekday (* = any, 0 = Sunday).
  */10 * * * *     every 10 minutes
  0 1 * * *        every day at 01:00
  0 3 * * 0        Sundays at 03:00
  0 1 1 * *        the 1st of every month at 01:00`}
	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List policies, when each runs and how many backups each keeps", Long: `KEEP is how many backups with that policy's tag each backup PC keeps.`, Example: `  sevjala policy list`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			ps, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) ([]localapi.Policy, error) {
				return cl.Policies(ctx)
			})
			if err != nil {
				return err
			}
			renderPolicies(c.OutOrStdout(), ps)
			return nil
		},
	})
	add, _ := policyEditCmd(g, true)
	set, _ := policyEditCmd(g, false)
	c.AddCommand(add, set, &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a policy; backups already made with it stay", Long: `Prefer 'policy set <name> --disable' for the built-in policies: it stops new backups
but keeps applying the policy's keep count.`, Example: `  sevjala policy remove weekly`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if _, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) (struct{}, error) {
				return struct{}{}, cl.RemovePolicy(ctx, args[0])
			}); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "removed %s; backups tagged %s are kept\n", args[0], args[0])
			return nil
		},
	})
	return c
}

type policyFlags struct {
	schedule        string
	keep            int
	to              []string
	enable, disable bool
}

// policyEditCmd builds 'policy add' or 'policy set' and returns the flags it
// fills, so tests can parse without running.
func policyEditCmd(g *globals, adding bool) (*cobra.Command, *policyFlags) {
	f := &policyFlags{}
	c := &cobra.Command{
		Use:   "set <name>",
		Short: "Change a policy; only the flags you give change",
		Example: `  sevjala policy set daily --keep 14
  sevjala policy set daily --schedule "30 2 * * *"      (every day at 02:30)
  sevjala policy set frequent --disable                 (pause; --enable resumes)
  sevjala policy set monthly --to BACKUP-PC-1           (or --to all)`,
		Args: cobra.ExactArgs(1),
	}
	if adding {
		c.Use, c.Short = "add <name> --keep N", "Add a policy"
		c.Example = `  sevjala policy add weekly --schedule "0 3 * * 0" --keep 8     (Sundays at 03:00)
  sevjala policy add yearend --keep 5                          (only via backup now --tag yearend)`
	}
	c.RunE = func(c *cobra.Command, args []string) error {
		ch, err := f.change(c.Flags(), adding)
		if err != nil {
			return err
		}
		p, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Policy, error) {
			if adding {
				ch.Name = args[0]
				return cl.AddPolicy(ctx, ch)
			}
			return cl.SetPolicy(ctx, args[0], ch)
		})
		if err != nil {
			return err
		}
		renderPolicies(c.OutOrStdout(), []localapi.Policy{p})
		return nil
	}
	fl := c.Flags()
	fl.StringVar(&f.schedule, "schedule", "", `cron "minute hour day month weekday" or @hourly, @daily, @weekly, @monthly; empty: only 'backup now --tag'`)
	fl.IntVar(&f.keep, "keep", 0, "how many backups with this tag each node keeps")
	fl.StringSliceVar(&f.to, "to", nil, "nodes to send to, comma separated, or all")
	if !adding {
		fl.BoolVar(&f.enable, "enable", false, "run on schedule again")
		fl.BoolVar(&f.disable, "disable", false, "stop running on schedule (retention still keeps its backups)")
	}
	return c, f
}

func (f *policyFlags) change(fs *pflag.FlagSet, adding bool) (localapi.PolicyChange, error) {
	var ch localapi.PolicyChange
	if f.enable && f.disable {
		return ch, usagef("use only one of --enable and --disable")
	}
	if adding && !fs.Changed("keep") {
		return ch, usagef("--keep is required: how many backups with this tag each node keeps")
	}
	if fs.Changed("schedule") {
		ch.Schedule = &f.schedule
	}
	if fs.Changed("keep") {
		ch.Keep = &f.keep
	}
	if fs.Changed("to") {
		ch.To = append([]string{}, f.to...)
	}
	if f.enable || f.disable {
		on := f.enable
		ch.Enabled = &on
	}
	if !adding && ch.Schedule == nil && ch.Keep == nil && ch.To == nil && ch.Enabled == nil {
		return ch, usagef("nothing to change: give --schedule, --keep, --to, --enable or --disable")
	}
	return ch, nil
}

func renderPolicies(w io.Writer, ps []localapi.Policy) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tWHEN\tNEXT RUN\tKEEP\tTO\tENABLED")
	for _, p := range ps {
		sched, next, to, on := scheduler.Describe(p.Schedule), "-", "all", "yes"
		if !p.NextRun.IsZero() {
			next = p.NextRun.Local().Format("2006-01-02 15:04")
		}
		if len(p.To) > 0 {
			hosts := make([]string, len(p.To))
			for i, t := range p.To {
				hosts[i] = t.Hostname
			}
			to = strings.Join(hosts, ",")
		}
		if !p.Enabled {
			on = "no"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", p.Name, sched, next, p.Keep, to, on)
	}
	tw.Flush()
}

func newRetentionCmd(g *globals) *cobra.Command {
	c := &cobra.Command{Use: "retention", Short: "Clean up old backups and fill in missing copies (controller)", Long: `Retention runs by itself every 5 minutes and when a backup PC comes back online. For
each backup PC it keeps, per policy, the newest backups that policy keeps, deletes the
rest, sends what is missing, and replaces damaged copies.

Safety rules: the last copy of a backup a PC still needs is never deleted; nothing is
deleted while the clock looks wrong (a backup dated in the future); a PC below its
free-space floor gets no new copies until space is freed.`}
	var dryRun bool
	run := &cobra.Command{
		Use:   "run",
		Short: "Clean up and fill in missing copies now (it also runs every 5 minutes)", Long: `Use --dry-run to see what would happen and why, without changing anything.`, Example: `  sevjala retention run --dry-run
  sevjala retention run`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			p, err := callAgent(g, 2*time.Minute, func(ctx context.Context, cl *localapi.Client) (localapi.RetentionPlan, error) {
				return cl.RunRetention(ctx, dryRun)
			})
			if err != nil {
				return err
			}
			renderPlan(c.OutOrStdout(), p)
			return nil
		},
	}
	run.Flags().BoolVar(&dryRun, "dry-run", false, "only print what would be done")
	c.AddCommand(run)
	return c
}

func renderPlan(w io.Writer, p localapi.RetentionPlan) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	lines := 0
	actions := func(verb string, as []localapi.RetentionAction) {
		for _, a := range as {
			id := a.BackupID
			if id == "" {
				id = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", verb, id, a.Hostname, a.Reason)
			lines++
		}
	}
	ids := func(verb string, ids []string) {
		for _, id := range ids {
			fmt.Fprintf(tw, "%s\t%s\t\t\n", verb, id)
			lines++
		}
	}
	actions("delete", p.Deletes)
	actions("send", p.Sends)
	ids("unstage", p.Unstaged)
	ids("expire", p.Expired)
	actions("keep", p.Kept)
	tw.Flush()
	switch {
	case lines == 0:
		fmt.Fprintln(w, "nothing to do")
	case p.DryRun:
		fmt.Fprintln(w, "dry run: nothing was changed")
	}
}
