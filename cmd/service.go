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
	c := &cobra.Command{Use: "service", Short: "Install and control the Sevjala Agent service"}
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
		return failf("%s", svc.AdminHint)
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
		Short: "Create the data dir and config, register and start the service",
		Args:  cobra.NoArgs,
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
	c.Flags().StringVar(&role, "role", "", "agent role: controller or node (required)")
	return c
}

func newServiceUninstallCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and unregister the service; the data dir is kept",
		Args:  cobra.NoArgs,
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
				return failf("not installed")
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
		Use:   "start",
		Short: "Start the service and wait for the agent",
		Args:  cobra.NoArgs,
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
			if err := s.Start(); err != nil {
				return failf("start service: %w", err)
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
		Use:   "stop",
		Short: "Stop the service",
		Args:  cobra.NoArgs,
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
			if err := s.Stop(); err != nil {
				return failf("stop service: %w", err)
			}
			fmt.Fprintln(c.OutOrStdout(), "stopped")
			return nil
		},
	}
}

func newServiceStatusCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the service state and whether the agent answers",
		Args:  cobra.NoArgs,
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
