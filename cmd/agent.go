package cmd

import (
	"errors"
	"io/fs"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/svc"
)

func newAgentCmd(g *globals) *cobra.Command {
	var role string
	c := &cobra.Command{
		Use:    "agent",
		Short:  "Run the agent in the foreground (the service runs this)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if role != "" && !config.ValidRole(role) {
				return usagef("--role must be %q or %q", config.RoleController, config.RoleNode)
			}
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			cfg, err := config.Load(config.Path(dataDir))
			switch {
			case err == nil:
			case errors.Is(err, fs.ErrNotExist) && role != "":
				cfg = config.Default(role)
			case errors.Is(err, fs.ErrNotExist):
				return failf("not installed: run 'sevjala service install --role %s|%s'", config.RoleController, config.RoleNode)
			default:
				return fail(err)
			}
			if role != "" {
				cfg.Role = role
			}
			a := &svc.Agent{DataDir: dataDir, Config: cfg, Version: version, Foreground: service.Interactive()}
			s, err := svc.New(dataDir, a)
			if err != nil {
				return fail(err)
			}
			if err := s.Run(); err != nil {
				return fail(err)
			}
			return nil
		},
	}
	c.Flags().StringVar(&role, "role", "", "override the configured role: controller or node")
	return c
}
