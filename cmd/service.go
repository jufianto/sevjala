package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/svc"
)

const agentWait = 10 * time.Second

func newServiceCmd(g *globals) *cobra.Command {
	c := &cobra.Command{Use: "service", Short: "Install, start, stop or remove the background service"}
	c.AddCommand(newServiceInstallCmd(g), newServiceUninstallCmd(g),
		newServiceStartCmd(g), newServiceStopCmd(g), newServiceStatusCmd(g))
	return c
}

func checkNotInstalled(s serviceStatuser) error {
	if _, err := s.Status(); errors.Is(err, service.ErrNotInstalled) {
		return nil
	}
	return errors.New("already installed; run 'sevjala service uninstall' first")
}

func requireAdmin() error {
	if !svc.IsAdmin() {
		return &needsAdmin{why: "this needs administrator rights"}
	}
	return nil
}

// logEvent writes one event to the agent log; called only while the agent is
// not running so two processes never rotate the same file.
func logEvent(dataDir, msg, event string, attrs ...any) {
	log, closer, err := logx.Open(config.LogDir(dataDir), false)
	if err != nil {
		return
	}
	log.Info(msg, append([]any{logx.Event(event)}, attrs...)...)
	closer.Close()
}

func printStatus(w io.Writer, s service.Service, dataDir string, st localapi.Status, apiErr error) statusView {
	v := buildStatusView(s, st, apiErr, dataDir, svc.IsAdmin())
	renderStatus(w, v)
	return v
}

func newServiceInstallCmd(g *globals) *cobra.Command {
	var role string
	c := &cobra.Command{
		Use:   "install --role controller|node",
		Short: "Install and start the service (setup does this for you)",
		Long: `Creates the data folder, opens the firewall and starts the service. 'sevjala setup'
runs this and the next steps for you.`,
		Example: `  sevjala service install --role controller     (the PC with the database)
  sevjala service install --role node           (a backup PC)`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if !config.ValidRole(role) {
				return usagef("--role must be %q or %q", config.RoleController, config.RoleNode)
			}
			if err := requireAdmin(); err != nil {
				return err
			}
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			out := c.OutOrStdout()
			s, err := svc.New(dataDir, &svc.Agent{})
			if err != nil {
				return fail(err)
			}
			if err := checkNotInstalled(s); err != nil {
				return fail(err)
			}
			if err := svc.LockDataDir(dataDir); err != nil {
				return fail(err)
			}
			created, err := svc.PrepareDataDir(dataDir, role)
			if err != nil {
				return fail(err)
			}
			if created {
				fmt.Fprintf(out, "wrote %s\n", config.Path(dataDir))
			}
			if runtime.GOOS != "windows" {
				sudoUser := os.Getenv("SUDO_USER")
				if err := svc.EnsureGroup(sudoUser); err != nil {
					return fail(err)
				}
				if sudoUser != "" {
					fmt.Fprintf(out, "added %s to group %s; log out and back in to use the CLI without sudo\n", sudoUser, config.SocketGroup)
				} else {
					fmt.Fprintf(out, "add operators to group %s to use the CLI without sudo\n", config.SocketGroup)
				}
			}
			cfg, err := config.Load(config.Path(dataDir))
			if err != nil {
				return fail(err)
			}
			if err := svc.AddFirewallRules(cfg.ListenPort(), cfg.DiscoveryPort); err != nil {
				return failf("firewall: %w", err)
			}
			if runtime.GOOS == "windows" {
				fmt.Fprintf(out, "opened the Windows firewall for %d/tcp and %d/udp\n", cfg.ListenPort(), cfg.DiscoveryPort)
			}
			exe, _ := os.Executable()
			if err := s.Install(); err != nil {
				return failf("install service: %w", err)
			}
			logEvent(dataDir, "service installed", logx.EventServiceInstalled, "role", role, "executable", exe)
			fmt.Fprintf(out, "installed service %s → %s\n", svc.Name, exe)
			if err := s.Start(); err != nil {
				return failf("start service: %w", err)
			}
			st, apiErr := waitForAgent(localapi.NewClient(localapi.Endpoint(dataDir)), agentWait)
			if v := printStatus(out, s, dataDir, st, apiErr); !v.OK() {
				return failf("agent did not answer within %s; see %s", agentWait, filepath.Join(config.LogDir(dataDir), logx.FileName))
			}
			return nil
		},
	}
	c.Flags().StringVar(&role, "role", "", "`ROLE`: controller (the PC with the database) or node (a backup PC); required")
	return c
}

func newServiceUninstallCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the service; backups and settings are kept",
		Long: `The data folder (backups, settings, pairing) stays, so a later install picks up
where it left off.`,
		Example: `  sevjala service uninstall`,
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := requireAdmin(); err != nil {
				return err
			}
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			s, err := svc.New(dataDir, &svc.Agent{})
			if err != nil {
				return fail(err)
			}
			if checkNotInstalled(s) == nil {
				return fail(errNotInstalled)
			}
			_ = s.Stop()
			logEvent(dataDir, "service uninstalled", logx.EventServiceUninstalled)
			if err := s.Uninstall(); err != nil {
				return failf("uninstall service: %w", err)
			}
			svc.RemoveFirewallRules()
			fmt.Fprintf(c.OutOrStdout(), "uninstalled service %s; data kept in %s\n", svc.Name, dataDir)
			return nil
		},
	}
}

func newServiceStartCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:     "start",
		Short:   "Start the service (nothing to do if it already runs)",
		Long:    `Starts the service and waits until it answers.`,
		Example: `  sevjala service start`,
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := requireAdmin(); err != nil {
				return err
			}
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			s, err := svc.New(dataDir, &svc.Agent{})
			if err != nil {
				return fail(err)
			}
			if _, err := startService(s); err != nil {
				return fail(err)
			}
			st, apiErr := waitForAgent(localapi.NewClient(localapi.Endpoint(dataDir)), agentWait)
			if !printStatus(c.OutOrStdout(), s, dataDir, st, apiErr).OK() {
				return errSilent1
			}
			return nil
		},
	}
}

func newServiceStopCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:     "stop",
		Short:   "Stop the service (nothing to do if it is already stopped)",
		Long:    `No backups are made or received while it is stopped.`,
		Example: `  sevjala service stop`,
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := requireAdmin(); err != nil {
				return err
			}
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			s, err := svc.New(dataDir, &svc.Agent{})
			if err != nil {
				return fail(err)
			}
			msg, err := stopService(s)
			if err != nil {
				return fail(err)
			}
			fmt.Fprintln(c.OutOrStdout(), msg)
			return nil
		},
	}
}

func newServiceStatusCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   "Show whether the service runs and answers",
		Long:    `Exit code 0 when the service runs and answers, 1 otherwise.`,
		Example: `  sevjala service status`,
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
			if !printStatus(c.OutOrStdout(), s, dataDir, st, apiErr).OK() {
				return errSilent1
			}
			return nil
		},
	}
}

var errNotInstalled = errors.New("this PC has no Sevjala service installed; install it with: sevjala setup")

type controllable interface {
	serviceStatuser
	Start() error
	Stop() error
}

// startService treats a running service as done, so setup and scripts can
// call it without checking first.
func startService(s controllable) (string, error) {
	st, err := s.Status()
	switch {
	case errors.Is(err, service.ErrNotInstalled):
		return "", errNotInstalled
	case err == nil && st == service.StatusRunning:
		return "already running", nil
	}
	if err := s.Start(); err != nil {
		return "", fmt.Errorf("could not start the Sevjala service: %w", err)
	}
	return "started", nil
}

func stopService(s controllable) (string, error) {
	st, err := s.Status()
	switch {
	case errors.Is(err, service.ErrNotInstalled):
		return "", errNotInstalled
	case err == nil && st == service.StatusStopped:
		return "already stopped", nil
	}
	if err := s.Stop(); err != nil {
		return "", fmt.Errorf("could not stop the Sevjala service: %w", err)
	}
	return "stopped", nil
}
