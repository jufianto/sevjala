package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

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
		var le *localapi.Error
		if errors.As(err, &le) {
			return zero, fail(le)
		}
		if errors.Is(err, fs.ErrPermission) {
			return zero, failf("the agent at %s refused this user: %s (%w)", ep, svc.AdminHint, err)
		}
		return zero, failf("agent not answering at %s (is the service running?): %w", ep, err)
	}
	return v, nil
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
		Short: "Find agents on the LAN (controller)",
		Args:  cobra.NoArgs,
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
		Short: "Show a one-time pairing code (node)",
		Args:  cobra.NoArgs,
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
		Short: "Pair with a discovered node using its code (controller)",
		Args:  cobra.ExactArgs(1),
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
	c.Flags().StringVar(&code, "code", "", "6-digit code from 'sevjala code' on the node")
	c.Flags().StringVar(&fp, "fp", "", "fingerprint from 'sevjala code' on the node")
	return c
}

func newAgentsCmd(g *globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "agents",
		Short: "List paired agents (controller)",
		Args:  cobra.NoArgs,
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
		Short: "Add a node by address when discovery cannot reach it (controller)",
		Args:  cobra.ExactArgs(1),
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
			free = humanBytes(a.FreeBytes)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", shortID(a.AgentID), a.Hostname, a.Addr, a.State, free, seen, a.Problem)
	}
	tw.Flush()
}

func newPingCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "ping <agent>",
		Short: "Check a paired node over mutual TLS (controller)",
		Args:  cobra.ExactArgs(1),
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
