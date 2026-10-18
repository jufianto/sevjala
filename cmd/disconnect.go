package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
)

func newDisconnectCmd(g *globals) *cobra.Command {
	var confirm string
	c := &cobra.Command{
		Use:   "disconnect",
		Short: "Forget this PC's controller; backups stop coming (run on the backup PC)",
		Long: `The backup PC forgets the controller it is paired with: that controller can no longer
reach it and shows it as rejected until it is removed there with 'sevjala agents remove'.
The backups on this PC stay; delete them with 'sevjala backups remove'. To pair again,
run 'sevjala code'. Needs administrator rights and this PC's name typed to confirm.`,
		Example: `  sevjala disconnect`,
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			d, err := withConfirm(c.InOrStdin(), c.OutOrStdout(), confirm, func(confirm string) (localapi.Disconnected, error) {
				return callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Disconnected, error) {
					return cl.Disconnect(ctx, confirm)
				})
			})
			if err != nil {
				return err
			}
			out := c.OutOrStdout()
			fmt.Fprintf(out, "Disconnected from %s.\n", d.Controller)
			if d.Backups > 0 {
				fmt.Fprintf(out, "This PC keeps its %s; to delete them: sevjala backups remove --all\n", plural(d.Backups, "backup"))
			}
			fmt.Fprintf(out, "On %s, forget this PC with: sevjala agents remove <this PC>. To pair again: sevjala code\n", d.Controller)
			return nil
		},
	}
	c.Flags().StringVar(&confirm, "confirm", "", "this PC's `NAME`, instead of typing it at the prompt")
	return c
}

func newBackupsRemoveCmd(g *globals) *cobra.Command {
	var from, confirm string
	var all bool
	c := &cobra.Command{
		Use:   "remove [<BACKUP>...] [--from <controller>] [--all]",
		Short: "Delete backups this backup PC holds (run on the backup PC)",
		Long: `Deletes backups from this PC: the ones named, every one from one controller (--from, its
name or agent id; for example a controller this PC is no longer paired with), or all of
them. The controller this PC is paired with sends its own backups again; run 'sevjala
disconnect' first to stop that. Needs administrator rights and this PC's name typed to
confirm.`,
		Example: `  sevjala backups remove --from OLD-SERVER
  sevjala backups remove --all
  sevjala backups remove BK-20261014-002000-30D3`,
		RunE: func(c *cobra.Command, args []string) error {
			req := localapi.RemoveHeld{IDs: args, From: from, All: all}
			if n := btoi(len(args) > 0) + btoi(from != "") + btoi(all); n != 1 {
				return usagef("give backup ids, --from <controller> or --all (one of them)")
			}
			r, err := withConfirm(c.InOrStdin(), c.OutOrStdout(), confirm, func(confirm string) (localapi.HeldRemoved, error) {
				req.Confirm = confirm
				return callAgent(g, time.Minute, func(ctx context.Context, cl *localapi.Client) (localapi.HeldRemoved, error) {
					return cl.RemoveHeld(ctx, req)
				})
			})
			if err != nil {
				return err
			}
			out := c.OutOrStdout()
			fmt.Fprintf(out, "Removed %s (%s) from this PC.\n", plural(len(r.Removed), "backup"), inventory.HumanBytes(r.Bytes))
			if r.Resend > 0 {
				fmt.Fprintf(out, "%d of them came from the controller this PC is paired with, which sends them again; to stop that: sevjala disconnect\n", r.Resend)
			}
			for _, f := range r.Failed {
				fmt.Fprintf(c.ErrOrStderr(), "not removed: %s\n", f)
			}
			if len(r.Failed) > 0 {
				return errSilent1
			}
			return nil
		},
	}
	c.Flags().StringVar(&from, "from", "", "every backup from this `CONTROLLER` (name or agent id)")
	c.Flags().BoolVar(&all, "all", false, "every backup this PC holds")
	c.Flags().StringVar(&confirm, "confirm", "", "this PC's `NAME`, instead of typing it at the prompt")
	return c
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
