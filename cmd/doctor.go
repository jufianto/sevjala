package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/lanapi"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/svc"
)

type checkResult struct {
	Name, Detail string
	OK           bool
	Warn         bool // not OK, but shown as WARN and does not fail doctor
	Skip         bool // not run: it needs administrator rights
}

func checkWritable(name, dir string) checkResult {
	f, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		return checkResult{Name: name, Detail: fmt.Sprintf("%s not writable: %v", dir, err)}
	}
	f.Close()
	_ = os.Remove(f.Name())
	return checkResult{Name: name, Detail: dir, OK: true}
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
		return checkResult{Name: "lan api", Detail: err.Error()}
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
	agent := checkResult{Name: "local api", Detail: fmt.Sprint(v.AgentErr)}
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
		switch {
		case r.OK:
		case r.Skip:
			mark = "skip"
		case r.Warn:
			mark = "WARN"
		default:
			mark, all = "FAIL", false
		}
		fmt.Fprintf(w, "%-6s%-12s%s\n", mark, r.Name, r.Detail)
	}
	return all
}

// databaseChecks reports what the agent sees through 'db detect'.
func databaseChecks(d localapi.Detection, err error) []checkResult {
	if err != nil {
		return []checkResult{{Name: "database", Detail: err.Error()}}
	}
	server := d.Flavour + " " + d.ServerVersion
	var parts []string
	for _, db := range d.Databases {
		if db.Configured {
			parts = append(parts, db.Name+" "+strings.TrimPrefix(db.Consistency, "--"))
		}
	}
	dbCheck := checkResult{Name: "database", Detail: server + " · " + strings.Join(parts, ", "), OK: true}
	if len(parts) == 0 {
		dbCheck = checkResult{Name: "database", Detail: server + "; nothing to back up yet: sevjala db set --db <name>", Warn: true}
	}
	return []checkResult{{Name: "dumper", Detail: d.Dumper, OK: true}, dbCheck}
}

func storeCheck(s *localapi.StoreInfo, minFree int64) checkResult {
	if s == nil {
		return checkResult{Name: "store", Detail: "the agent reported no store"}
	}
	detail := fmt.Sprintf("%s · %s free · %d backups", s.Path, inventory.HumanBytes(s.FreeBytes), s.Backups)
	switch {
	case s.FreeBytes < minFree:
		return checkResult{Name: "store", Detail: detail + " · under the " + inventory.HumanBytes(minFree) + " floor"}
	case len(s.Problems) > 0:
		return checkResult{Name: "store", Detail: detail + " · " + strings.Join(s.Problems, "; "), Warn: true}
	}
	return checkResult{Name: "store", Detail: detail, OK: true}
}

// scheduleCheck shows the next scheduled run; databases is how many are configured.
func scheduleCheck(ps []localapi.Policy, err error, databases int) checkResult {
	r := checkResult{Name: "schedule"}
	if err != nil {
		r.Detail = err.Error()
		return r
	}
	if databases == 0 {
		r.Detail, r.Warn = "idle: no database configured (sevjala db set --db <name>)", true
		return r
	}
	var next time.Time
	var names []string
	for _, p := range ps {
		switch {
		case !p.Enabled || p.NextRun.IsZero():
		case next.IsZero() || p.NextRun.Before(next):
			next, names = p.NextRun, []string{p.Name}
		case p.NextRun.Equal(next):
			names = append(names, p.Name)
		}
	}
	if next.IsZero() {
		r.Detail, r.Warn = "no enabled policy has a schedule (sevjala policy list)", true
		return r
	}
	r.Detail, r.OK = fmt.Sprintf("next run %s (%s)", next.Local().Format("2006-01-02 15:04"), strings.Join(names, ", ")), true
	return r
}

// nodesCheck fails without a paired node and warns about nodes that are offline or in error.
func nodesCheck(as []localapi.Agent, err error) checkResult {
	r := checkResult{Name: "nodes"}
	if err != nil {
		r.Detail = err.Error()
		return r
	}
	var bad []string
	nodes := 0
	for _, a := range as {
		if a.Role != config.RoleNode {
			continue
		}
		nodes++
		if a.State == "offline" || a.State == "error" {
			bad = append(bad, fmt.Sprintf("%s %s (%s)", a.Hostname, a.State, a.Problem))
		}
	}
	switch {
	case nodes == 0:
		r.Detail = "no paired node: sevjala discover, then sevjala pair"
	case len(bad) > 0:
		r.Detail, r.Warn = strings.Join(bad, "; "), true
	default:
		r.Detail, r.OK = fmt.Sprintf("%d online", nodes), true
	}
	return r
}

func newDoctorCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check everything and say what is wrong",
		Long: `Checks the data folder, settings, service, network, database, schedule and backup PCs.
Each line is ok, WARN or FAIL with the reason; exit code 1 when something fails.`,
		Example: `  sevjala doctor`,
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
			endpoint := localapi.Endpoint(dataDir)
			st, apiErr := queryAgent(localapi.NewClient(endpoint))
			admin := svc.IsAdmin()
			v := buildStatusView(s, st, apiErr, dataDir, admin)
			// The data folder is locked to administrators (D-010): a normal user's
			// doctor runs every check that goes through the agent.
			var rs []checkResult
			var cfg *config.Config
			if admin {
				cfgCheck, loaded := checkConfig(dataDir)
				idCheck, id := checkIdentity(dataDir)
				rs = append(rs, checkWritable("data dir", dataDir), checkLockdown(dataDir), cfgCheck, idCheck)
				if cfg = loaded; cfg != nil {
					rs = append(rs, checkLAN(*cfg, id))
				}
			} else {
				rs = append(rs, checkResult{Name: "data dir", Skip: true,
					Detail: "needs administrator rights: run 'sevjala doctor' as administrator for these"})
			}
			rs = append(rs, serviceChecks(v, endpoint)...)
			role, dbs, minFree := st.Role, 0, int64(0)
			if cfg != nil {
				role, dbs, minFree = cfg.Role, len(cfg.Controller.Database.Databases), cfg.Node.Store.MinFreeBytes
			} else if st.Health != nil {
				dbs = len(st.Health.Databases)
			}
			if apiErr == nil {
				switch role {
				case config.RoleController:
					if admin {
						rs = append(rs, checkWritable("staging", config.StagingDir(dataDir)))
					}
					cl := localapi.NewClient(endpoint)
					d, err := withTimeout(15*time.Second, cl.DetectDatabase)
					ps, perr := withTimeout(5*time.Second, cl.Policies)
					as, aerr := withTimeout(5*time.Second, cl.Agents)
					rs = append(rs, databaseChecks(d, plain(err))...)
					rs = append(rs, scheduleCheck(ps, plain(perr), dbs), nodesCheck(as, plain(aerr)))
				case config.RoleNode:
					rs = append(rs, storeCheck(st.Store, minFree))
				}
			}
			if runtime.GOOS == "windows" {
				rs = append(rs, checkFirewall())
			}
			ok := renderChecks(c.OutOrStdout(), rs)
			if !admin && apiErr != nil {
				fmt.Fprintln(c.OutOrStdout(), "\nrun 'sevjala doctor' as administrator for the full check")
			}
			if !ok {
				return errSilent1
			}
			return nil
		},
	}
}

// withTimeout gives each doctor query its own limit, so a slow database does
// not fail the checks after it.
func withTimeout[T any](d time.Duration, f func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return f(ctx)
}

func plain(err error) error {
	if err == nil {
		return nil
	}
	return agentProblem(err, nil)
}
