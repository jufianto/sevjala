package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/lanapi"
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

func checkConfig(dataDir string) (checkResult, *config.Config) {
	cfg, err := config.Load(config.Path(dataDir))
	if err != nil {
		return checkResult{Name: "config", Detail: err.Error()}, nil
	}
	return checkResult{Name: "config", Detail: "role=" + cfg.Role, OK: true}, &cfg
}

func checkLockdown(dataDir string) checkResult {
	if err := svc.CheckDataDirLocked(dataDir); err != nil {
		return checkResult{Name: "lockdown", Detail: err.Error()}
	}
	return checkResult{Name: "lockdown", Detail: "admin/root only", OK: true}
}

func checkIdentity(dataDir string) (checkResult, *identity.Identity) {
	id, err := identity.Load(dataDir)
	if err != nil {
		return checkResult{Name: "identity", Detail: err.Error()}, nil
	}
	return checkResult{Name: "identity", Detail: shortID(id.AgentID) + "  " + id.FP, OK: true}, id
}

// checkLAN asks our own LAN API for /identity and expects our fingerprint.
func checkLAN(cfg config.Config, id *identity.Identity) checkResult {
	host, _, _ := net.SplitHostPort(cfg.Listen)
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, strconv.Itoa(cfg.ListenPort()))
	if id == nil {
		return checkResult{Name: "lan api", Detail: addr + ": no identity to compare with"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	info, err := lanapi.NewClient(id).Identity(ctx, addr)
	if err != nil {
		return checkResult{Name: "lan api", Detail: fmt.Sprintf("%s: %v", addr, err)}
	}
	if info.FP != id.FP {
		return checkResult{Name: "lan api", Detail: fmt.Sprintf("%s answers as %s, not this agent", addr, info.FP)}
	}
	return checkResult{Name: "lan api", Detail: addr + " → " + info.Role, OK: true}
}

func checkFirewall() checkResult {
	if err := svc.FirewallRulesPresent(); err != nil {
		return checkResult{Name: "firewall", Detail: err.Error()}
	}
	return checkResult{Name: "firewall", Detail: svc.FirewallRuleLAN + ", " + svc.FirewallRuleDiscovery, OK: true}
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
		Short: "Check the data dir, config, identity, service, local and LAN APIs",
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
			cfgCheck, cfg := checkConfig(dataDir)
			idCheck, id := checkIdentity(dataDir)
			rs := []checkResult{checkDataDir(dataDir), checkLockdown(dataDir), cfgCheck, idCheck}
			rs = append(rs, serviceChecks(v, endpoint)...)
			if cfg != nil {
				rs = append(rs, checkLAN(*cfg, id))
			}
			if runtime.GOOS == "windows" {
				rs = append(rs, checkFirewall())
			}
			if !renderChecks(c.OutOrStdout(), rs) {
				return errSilent1
			}
			return nil
		},
	}
}
