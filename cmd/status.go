package cmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/svc"
)

type serviceStatuser interface {
	Status() (service.Status, error)
	Platform() string
}

type statusView struct {
	Service   string
	ServiceUp bool
	Platform  string
	Agent     *localapi.Status
	AgentErr  error
	DataDir   string
}

func (v statusView) OK() bool { return v.ServiceUp && v.Agent != nil }

// buildStatusView treats an answering agent as proof the service runs when an
// unprivileged caller cannot see the service manager's state (macOS launchctl).
func buildStatusView(s serviceStatuser, st localapi.Status, apiErr error, dataDir string, privileged bool) statusView {
	v := statusView{Platform: s.Platform(), DataDir: dataDir}
	if apiErr == nil {
		v.Agent = &st
	} else {
		v.AgentErr = agentProblem(apiErr, s)
	}
	state, err := s.Status()
	switch {
	case errors.Is(err, service.ErrNotInstalled):
		v.Service = "not installed"
	case err != nil:
		v.Service = "unknown (" + err.Error() + ")"
	case state == service.StatusRunning:
		v.Service, v.ServiceUp = "running", true
	case !privileged && v.Agent != nil:
		v.Service, v.ServiceUp = "unknown (run with sudo for the service state)", true
	default:
		v.Service = "stopped"
	}
	return v
}

func renderStatus(w io.Writer, v statusView) {
	fmt.Fprintf(w, "%-10s%-12s (%s)\n", "service", v.Service, v.Platform)
	if v.Agent != nil {
		a := v.Agent
		fmt.Fprintf(w, "%-10s%-12s %s  role=%s  up %s  pid %d\n", "agent", "answering",
			a.Version, a.Role, time.Duration(a.UptimeS)*time.Second, a.PID)
	} else {
		fmt.Fprintf(w, "%-10snot answering: %v\n", "agent", v.AgentErr)
	}
	fmt.Fprintf(w, "%-10s%s\n", "data dir", v.DataDir)
}

func queryAgent(c *localapi.Client) (localapi.Status, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return c.Status(ctx)
}

func waitForAgent(c *localapi.Client, timeout time.Duration) (localapi.Status, error) {
	deadline := time.Now().Add(timeout)
	for {
		st, err := queryAgent(c)
		if err == nil || time.Now().After(deadline) {
			return st, err
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// renderOverview is 'sevjala status': the verdict and what needs attention
// first, then the facts. ok is false for a problem or an agent that does not
// answer.
func renderOverview(w io.Writer, v statusView, host string, now time.Time) bool {
	a := v.Agent
	if a == nil {
		fmt.Fprintf(w, "Sevjala on %s: PROBLEM\n  ! %v\n\n", host, v.AgentErr)
		fmt.Fprintf(w, "  %-12s%s (%s)\n", "service", v.Service, v.Platform)
		return false
	}
	h := a.Health
	if h == nil {
		h = &localapi.Health{Level: localapi.HealthWarning, Items: []localapi.HealthItem{
			{Level: localapi.HealthWarning, Text: "the agent is older than this command; run: sevjala update"}}}
	}
	role := a.Role
	if role == config.RoleNode {
		role = "backup PC"
	}
	fmt.Fprintf(w, "Sevjala %s on %s: %s\n", role, cmp.Or(a.Hostname, host), strings.ToUpper(h.Level))
	if len(h.Items) == 0 {
		fmt.Fprintf(w, "  %s\n", h.Headline)
	}
	for _, it := range h.Items {
		fmt.Fprintf(w, "  ! %s\n", it.Text)
	}
	line := func(label, format string, args ...any) {
		fmt.Fprintf(w, "  %-12s%s\n", label, fmt.Sprintf(format, args...))
	}
	fmt.Fprintln(w)
	line("service", "%s (%s), agent %s, up %s", v.Service, v.Platform, a.Version, upFor(time.Duration(a.UptimeS)*time.Second))
	for _, d := range h.Databases {
		if d.LastGood == "" {
			line(d.Name, "no backup on a backup PC yet")
			continue
		}
		line(d.Name, "last backup %s, %s, on %d of %d backup PCs", localapi.When(d.LastGoodAt, now), inventory.HumanBytes(d.Size), d.Copies, d.Planned)
	}
	if !h.NextBackupAt.IsZero() {
		line("next", "%s (%s)", localapi.Clock(h.NextBackupAt, now), h.NextPolicy)
	}
	if len(h.Agents) > 0 {
		pcs := make([]string, len(h.Agents))
		for i, ag := range h.Agents {
			pcs[i] = ag.Hostname + " " + ag.State
			if ag.State != "online" && !ag.LastSeen.IsZero() {
				pcs[i] += " since " + localapi.Clock(ag.LastSeen, now)
			}
		}
		line("backup PCs", "%s", strings.Join(pcs, " · "))
	}
	switch {
	case a.Store != nil:
		line("holds", "%d backups, %s free", a.Store.Backups, inventory.HumanBytes(a.Store.FreeBytes))
	case a.Role == config.RoleController:
		line("disk", "%s free here", inventory.HumanBytes(h.FreeBytes))
	}
	return h.Level != localapi.HealthProblem
}

func upFor(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func newStatusCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Is the data protected? One screen: last backups, backup PCs, what needs attention",
		Long: `Shows whether backups are healthy: OK, WARNING or PROBLEM, what needs attention, the
last backup of each database and where its copies are, the next scheduled backup, the
backup PCs and free space. Needs no administrator rights.
Exit code 0 for OK or WARNING, 1 for a PROBLEM or when the service does not answer.`,
		Example: `  sevjala status`,
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			s, err := svc.New(dataDir, &svc.Agent{})
			if err != nil {
				return fail(err)
			}
			st, apiErr := queryAgent(localapi.NewClient(localapi.Endpoint(dataDir)))
			host, _ := os.Hostname()
			if !renderOverview(c.OutOrStdout(), buildStatusView(s, st, apiErr, dataDir, svc.IsAdmin()), host, time.Now()) {
				return errSilent1
			}
			return nil
		},
	}
}
