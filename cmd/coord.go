package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/pairing"
	"github.com/jufianto/sevjala/svc"
)

// callAgent runs one local API call; an answer with an error is the agent's
// message, anything else means the agent is not reachable.
func callAgent[T any](g *globals, timeout time.Duration, f func(context.Context, *localapi.Client) (T, error)) (T, error) {
	var zero T
	dataDir, err := g.dataDir()
	if err != nil {
		return zero, fail(err)
	}
	ep := localapi.Endpoint(dataDir)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	v, err := f(ctx, localapi.NewClient(ep))
	if err != nil {
		var s serviceStatuser
		if ss, serr := svc.New(dataDir, &svc.Agent{}); serr == nil {
			s = ss
		}
		return zero, fail(agentProblem(err, s))
	}
	return v, nil
}

// agentProblem says what to do when a local API call fails; an answer with an
// error is the agent's own message. s may be nil when the state is unknown.
func agentProblem(err error, s serviceStatuser) error {
	var le *localapi.Error
	switch {
	case errors.As(err, &le):
		return le
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("this account may not use Sevjala: %s", svc.AdminHint)
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("the Sevjala service did not answer in time; it may be busy (a slow database?). Try again, or run: sevjala doctor")
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, syscall.ECONNRESET):
		return errors.New("the Sevjala service stopped answering mid-way (did it restart?); try again")
	}
	if s != nil {
		st, serr := s.Status()
		switch {
		case errors.Is(serr, service.ErrNotInstalled):
			return errNotInstalled
		case serr == nil && st == service.StatusRunning:
			return errors.New("the Sevjala service is running but not answering yet; wait a few seconds, or run: sevjala doctor")
		case serr == nil:
			return errors.New("the Sevjala service is stopped; start it with: sevjala service start")
		}
	}
	return errors.New("the Sevjala service is not running; start it with: sevjala service start (or install it with: sevjala setup)")
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func newDiscoverCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "discover",
		Short: "Find Sevjala PCs on this network (controller)",
		Long: `Sends one broadcast on every local network and lists the Sevjala PCs that answer.
It does not cross Tailscale, VPNs or other subnets: add those with 'sevjala agents add <IP>'.`,
		Example: `  sevjala discover
  sevjala agents add 192.168.1.21      (a PC discovery does not reach)`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			ds, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) ([]localapi.Discovered, error) {
				return cl.Discover(ctx)
			})
			if err != nil {
				return err
			}
			renderDiscovered(c.OutOrStdout(), ds)
			return nil
		},
	}
}

func renderDiscovered(w io.Writer, ds []localapi.Discovered) {
	if len(ds) == 0 {
		fmt.Fprintln(w, "no agents answered. Discovery only reaches this network: for a node elsewhere (Tailscale, VPN,\n"+
			"another subnet) run 'sevjala agents add <its IP>', then the 'sevjala pair …' line its 'sevjala code' shows.\n"+
			"Otherwise check the firewall on the nodes.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "AGENT\tHOSTNAME\tADDRESS\tROLE\tVERSION\tPAIRED")
	for _, d := range ds {
		paired := "no"
		if d.Paired {
			paired = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", shortID(d.AgentID), d.Hostname, d.Addr, d.Role, d.Version, paired)
	}
	tw.Flush()
}

func newCodeCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "code",
		Short: "Show a one-time pairing code (run on the backup PC)",
		Long: `Prints a 6-digit code, valid 10 minutes, and this PC's fingerprint, plus the exact
'sevjala pair' line to type on the controller.`,
		Example: `  sevjala code`,
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			code, err := callAgent(g, 5*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Code, error) {
				return cl.Code(ctx)
			})
			if err != nil {
				return err
			}
			renderCode(c.OutOrStdout(), code)
			return nil
		},
	}
}

func renderCode(w io.Writer, c localapi.Code) {
	fp := pairing.ShortFP(c.FP)
	fmt.Fprintf(w, "pairing code %s, valid until %s\n", c.Code, c.ExpiresAt.Local().Format("15:04"))
	fmt.Fprintf(w, "fingerprint  %s\n", fp)
	fmt.Fprintf(w, "on the controller run: sevjala pair %s --code %s --fp %s\n", shortID(c.AgentID), c.Code, fp)
	fmt.Fprintln(w, "(if sevjala discover there does not list this PC, first run: sevjala agents add <this PC's IP>)")
}

func newPairCmd(g *globals) *cobra.Command {
	var code, fp string
	c := &cobra.Command{
		Use:   "pair <agent> --code <code> --fp <fingerprint>",
		Short: "Pair with a backup PC using the code it shows (controller)",
		Long: `Type the line that 'sevjala code' printed on the backup PC. The PC must have been found
first, by 'sevjala discover' or 'sevjala agents add <IP>'.`,
		Example: `  sevjala discover
  sevjala pair 78ce929d --code 123456 --fp 3f2a9b1c77de0a41`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if !pairing.ValidCode(code) {
				return usagef("--code must be the 6-digit code shown by 'sevjala code' on the node")
			}
			if _, ok := pairing.ParseFP(fp); !ok {
				return usagef("--fp must be the %d-character fingerprint shown by 'sevjala code' on the node", pairing.FPChars)
			}
			a, err := callAgent(g, 20*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Agent, error) {
				return cl.Pair(ctx, args[0], code, fp)
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "paired with %s (%s) %s\n", a.Hostname, shortID(a.AgentID), a.FP)
			return nil
		},
	}
	c.Flags().StringVar(&code, "code", "", "the 6-digit `CODE` shown by 'sevjala code' on the backup PC")
	c.Flags().StringVar(&fp, "fp", "", "the `FINGERPRINT` shown by 'sevjala code' on the backup PC")
	return c
}

func newAgentsCmd(g *globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "agents",
		Short: "List paired backup PCs and their state (controller)",
		Long: `STATE is online, offline or warning; FREE is the disk space left on that PC; PROBLEM says
what is wrong when it is not online.`,
		Example: `  sevjala agents
  sevjala agents add 192.168.1.21
  sevjala agents remove 78ce929d`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			as, err := callAgent(g, 5*time.Second, func(ctx context.Context, cl *localapi.Client) ([]localapi.Agent, error) {
				return cl.Agents(ctx)
			})
			if err != nil {
				return err
			}
			renderAgents(c.OutOrStdout(), as)
			return nil
		},
	}
	c.AddCommand(&cobra.Command{
		Use:   "add <host[:port]>",
		Short: "Add a backup PC by its IP address when discovery cannot find it (controller)",
		Long: `For a PC on Tailscale, a VPN or another subnet. Afterwards pair it with the line that
'sevjala code' shows on it. For a PC already paired, this updates its address.`,
		Example: `  sevjala agents add 192.168.1.21
  sevjala agents add 192.168.1.21:17891`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			d, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Discovered, error) {
				return cl.AddAgent(ctx, args[0])
			})
			if err != nil {
				return err
			}
			out := c.OutOrStdout()
			renderDiscovered(out, []localapi.Discovered{d})
			if !d.Paired {
				fmt.Fprintf(out, "pair it with: sevjala pair %s --code <code> --fp <fingerprint> (both from 'sevjala code' on that PC)\n", shortID(d.AgentID))
			}
			return nil
		},
	})
	var force bool
	remove := &cobra.Command{
		Use:   "remove <agent> [--force]",
		Short: "Stop backing up to a backup PC; its files stay on that PC (controller)",
		Long: `The controller forgets the PC. Nothing is deleted on it. Refused while a policy sends
only to that PC, and, without --force, while some backup exists only there.`,
		Example: `  sevjala agents remove 78ce929d
  sevjala agents remove BACKUP-PC-1 --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			a, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Agent, error) {
				return cl.RemoveAgent(ctx, args[0], force)
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "removed %s (%s); its backup files stay on that PC.\n"+
				"To use it again: sevjala agents add <its IP>, then the pair line from 'sevjala code' on it.\n",
				a.Hostname, shortID(a.AgentID))
			return nil
		},
	}
	remove.Flags().BoolVar(&force, "force", false, "remove even if some backups have their only copy on that PC")
	c.AddCommand(remove)
	return c
}

func renderAgents(w io.Writer, as []localapi.Agent) {
	if len(as) == 0 {
		fmt.Fprintln(w, "no paired agents; run 'sevjala discover', then 'sevjala pair <agent> --code <code> --fp <fingerprint>'")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "AGENT\tHOSTNAME\tADDRESS\tSTATE\tFREE\tLAST SEEN\tPROBLEM")
	for _, a := range as {
		seen, free := "never", "-"
		if !a.LastSeen.IsZero() {
			seen = a.LastSeen.Local().Format("2006-01-02 15:04")
		}
		if a.FreeBytes > 0 {
			free = inventory.HumanBytes(a.FreeBytes)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", shortID(a.AgentID), a.Hostname, a.Addr, a.State, free, seen, a.Problem)
	}
	tw.Flush()
}

func newPingCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "ping <agent>",
		Short: "Check that a paired backup PC answers (controller)",
		Long:  `Connects to the PC over the encrypted link and prints how long it took to answer.`,
		Example: `  sevjala ping 78ce929d
  sevjala ping BACKUP-PC-1`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			p, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Ping, error) {
				return cl.Ping(ctx, args[0])
			})
			if err != nil {
				return err
			}
			renderPing(c.OutOrStdout(), p)
			return nil
		},
	}
}

func renderPing(w io.Writer, p localapi.Ping) {
	a := p.Agent
	fmt.Fprintf(w, "%s (%s) answered in %d ms: %s %s, %s, up %s\n", a.Hostname, a.Addr, p.RTTMs, a.Role, a.Version,
		p.Health, time.Duration(p.UptimeS)*time.Second)
}
