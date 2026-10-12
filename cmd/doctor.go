package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/svc"
)

type checkResult struct {
	Name, Detail string
	OK           bool
}

func checkDataDir(dir string) checkResult {
	f, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		return checkResult{Name: "data dir", Detail: fmt.Sprintf("%s not writable: %v", dir, err)}
	}
	f.Close()
	_ = os.Remove(f.Name())
	return checkResult{Name: "data dir", Detail: dir, OK: true}
}

func checkConfig(dataDir string) checkResult {
	cfg, err := config.Load(config.Path(dataDir))
	if err != nil {
		return checkResult{Name: "config", Detail: err.Error()}
	}
	return checkResult{Name: "config", Detail: "role=" + cfg.Role, OK: true}
}

func serviceChecks(v statusView, endpoint string) []checkResult {
	agent := checkResult{Name: "local api", Detail: fmt.Sprintf("%s: %v", endpoint, v.AgentErr)}
	if v.Agent != nil {
		agent = checkResult{Name: "local api", Detail: endpoint + " → " + v.Agent.Version, OK: true}
	}
	return []checkResult{
		{Name: "service", Detail: v.Service + " (" + v.Platform + ")", OK: v.ServiceUp},
		agent,
	}
}

func renderChecks(w io.Writer, rs []checkResult) bool {
	all := true
	for _, r := range rs {
		mark := "ok"
		if !r.OK {
			mark, all = "FAIL", false
		}
		fmt.Fprintf(w, "%-6s%-12s%s\n", mark, r.Name, r.Detail)
	}
	return all
}

func newDoctorCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the data dir, config, service and local API",
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
			endpoint := localapi.Endpoint(dataDir)
			st, apiErr := queryAgent(localapi.NewClient(endpoint))
			v := buildStatusView(s, st, apiErr, dataDir, svc.IsAdmin())
			rs := append([]checkResult{checkDataDir(dataDir), checkConfig(dataDir)}, serviceChecks(v, endpoint)...)
			if !renderChecks(c.OutOrStdout(), rs) {
				return errSilent1
			}
			return nil
		},
	}
}
