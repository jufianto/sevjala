package coord

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/discovery"
	"github.com/jufianto/sevjala/dump"
	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/lanapi"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/pairing"
	"github.com/jufianto/sevjala/store"
	"github.com/jufianto/sevjala/transfer"
)

type Controller struct {
	Self      *identity.Identity
	Store     *store.Store
	LAN       *lanapi.Client
	Targets   func() ([]*net.UDPAddr, error)
	Wait      time.Duration
	Log       *slog.Logger
	DataDir   string
	Staging   string
	Config    config.Config   // after start, read with config() and replaced by SetDatabase
	Ctx       context.Context // the agent's lifetime: dumps outlive the request that started them
	FindTools func(dumper string) (dump.Tools, error)
	Pool      *transfer.Pool

	mu       sync.Mutex
	seen     map[string]localapi.Discovered // from discover and agents add; pair needs the fp seen here
	jobs     map[string]*job                // dumps not yet in the store
	running  map[string]string              // database → backup_id being dumped
	failedAt map[string]time.Time           // database → creation time of its last failed dump

	cfgMu sync.Mutex

	PollEvery      time.Duration // Run's /status poll interval
	ReconcileEvery time.Duration // Run's reconciliation interval
	PollTimeout    time.Duration // per /status call; 0 means 10 s
	pollMu         sync.Mutex
	nodes          map[string]*nodeState

	reconcileMu sync.Mutex
	unknownSeen map[[2]string]bool // backup, agent: unknown backups already logged
}

func (c *Controller) config() config.Config {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	return c.Config
}

const (
	seenHint   = "run 'sevjala discover' or 'sevjala agents add <host:port>' first"
	pairedHint = "see 'sevjala agents' for paired agents"
)

func (c *Controller) Discover(ctx context.Context) ([]localapi.Discovered, error) {
	targets, err := c.Targets()
	if err != nil {
		return nil, err
	}
	replies, err := discovery.Probe(ctx, targets, c.Wait)
	if err != nil {
		return nil, err
	}
	out := make([]localapi.Discovered, 0, len(replies))
	for _, r := range replies {
		if r.AgentID == c.Self.AgentID {
			continue
		}
		d := localapi.Discovered{AgentID: r.AgentID, Hostname: r.Hostname, Addr: net.JoinHostPort(r.IP, strconv.Itoa(r.Port)),
			Role: r.Role, Version: r.Version, OS: r.OS, FP: r.FP}
		a, err := c.Store.Agent(ctx, r.AgentID)
		switch {
		case err == nil && a.FP == r.FP:
			d.Paired = true
			if err := c.Store.UpdateAddress(ctx, a.AgentID, r.Hostname, r.IP, r.Port); err != nil {
				return nil, err
			}
		case err != nil && !errors.Is(err, store.ErrNotFound):
			return nil, err
		}
		out = append(out, d)
	}
	c.remember(out...)
	c.Log.Info("discovery run", logx.Event(logx.EventDiscoveryRun), "found", len(out), "targets", len(targets))
	return out, nil
}

func (c *Controller) AddAgent(ctx context.Context, addr string) (localapi.Discovered, error) {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, strconv.Itoa(config.DefaultLANPort))
	}
	info, err := c.LAN.Identity(ctx, addr)
	if err != nil {
		return localapi.Discovered{}, localapi.Errorf(http.StatusBadGateway, "%v", err)
	}
	if info.AgentID == c.Self.AgentID {
		return localapi.Discovered{}, localapi.Errorf(http.StatusBadRequest, "%s is this controller", addr)
	}
	d := localapi.Discovered{AgentID: info.AgentID, Hostname: info.Hostname, Addr: addr, Role: info.Role,
		Version: info.Version, FP: info.FP}
	a, err := c.Store.Agent(ctx, info.AgentID)
	switch {
	case err == nil && a.FP == info.FP:
		d.Paired = true
		host, portStr, _ := net.SplitHostPort(addr)
		port, _ := strconv.Atoi(portStr)
		if err := c.Store.UpdateAddress(ctx, a.AgentID, info.Hostname, host, port); err != nil {
			return localapi.Discovered{}, err
		}
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return localapi.Discovered{}, err
	}
	c.remember(d)
	c.Log.Info("agent added", logx.Event(logx.EventAgentAdded), "agent_id", d.AgentID, "addr", addr)
	return d, nil
}

func (c *Controller) Pair(ctx context.Context, agent, code, fp string) (localapi.Agent, error) {
	if !pairing.ValidCode(code) {
		return localapi.Agent{}, localapi.Errorf(http.StatusBadRequest, "the code must be the 6 digits shown by 'sevjala code' on the node")
	}
	prefix, ok := pairing.ParseFP(fp)
	if !ok {
		return localapi.Agent{}, localapi.Errorf(http.StatusBadRequest,
			"the fingerprint must be the %d hex characters shown by 'sevjala code' on the node", pairing.FPChars)
	}
	c.mu.Lock()
	seen := slices.Collect(maps.Values(c.seen))
	c.mu.Unlock()
	d, err := resolve(seen, agent, func(d localapi.Discovered) (string, string) { return d.AgentID, d.Hostname }, seenHint)
	if err != nil {
		return localapi.Agent{}, err
	}
	if d.Role != config.RoleNode {
		return localapi.Agent{}, localapi.Errorf(http.StatusConflict, "%s is a %s; only nodes can be paired", d.Hostname, d.Role)
	}
	if !pairing.MatchFP(d.FP, prefix) {
		c.Log.Warn("pairing refused", logx.Event(logx.EventPairRejected), "agent_id", d.AgentID, "addr", d.Addr,
			"reason", "fingerprint mismatch")
		return localapi.Agent{}, localapi.Errorf(http.StatusConflict,
			"the agent found at %s has fingerprint %s, not the one shown by 'sevjala code'; do not pair: another device may be answering for it",
			d.Addr, pairing.ShortFP(d.FP))
	}
	if _, err := c.LAN.Pair(ctx, d.Addr, d.FP, code); err != nil {
		c.Log.Warn("pairing failed", logx.Event(logx.EventPairRejected), "agent_id", d.AgentID, "addr", d.Addr, "err", err)
		if errors.Is(err, lanapi.ErrRejected) {
			return localapi.Agent{}, localapi.Errorf(http.StatusBadGateway,
				"pair %s: it has no active pairing code (expired after 10 minutes, already used, burnt by %d wrong codes, or the node restarted); run 'sevjala code' on it again",
				d.Hostname, pairing.MaxAttempts)
		}
		return localapi.Agent{}, localapi.Errorf(http.StatusBadGateway, "pair %s: %v", d.Hostname, err)
	}
	host, portStr, _ := net.SplitHostPort(d.Addr)
	port, _ := strconv.Atoi(portStr)
	now := time.Now().UTC().Truncate(time.Second)
	a := store.Agent{AgentID: d.AgentID, Hostname: d.Hostname, LastIP: host, Port: port, FP: d.FP,
		Role: d.Role, Version: d.Version, PairedAt: now, LastSeen: now, State: "online"}
	if err := c.Store.UpsertAgent(ctx, a); err != nil {
		return localapi.Agent{}, err
	}
	d.Paired = true
	c.remember(d)
	c.Log.Info("paired with node", logx.Event(logx.EventPairSucceeded), "agent_id", a.AgentID, "hostname", a.Hostname, "fp", a.FP)
	return toAPI(a), nil
}

// RemoveAgent forgets a paired node. It refuses while a policy sends only to
// that node, and, unless forced, while a backup's only copy is there: the
// files stay on the node but this controller stops tracking them.
func (c *Controller) RemoveAgent(ctx context.Context, agent string, force bool) (localapi.Agent, error) {
	c.reconcileMu.Lock()
	defer c.reconcileMu.Unlock()
	as, err := c.Store.Agents(ctx)
	if err != nil {
		return localapi.Agent{}, err
	}
	a, err := resolve(as, agent, func(a store.Agent) (string, string) { return a.AgentID, a.Hostname }, pairedHint)
	if err != nil {
		return localapi.Agent{}, err
	}
	ps, err := c.Store.Policies(ctx)
	if err != nil {
		return localapi.Agent{}, err
	}
	for _, p := range ps {
		if slices.Equal(p.Destinations, []string{a.AgentID}) {
			return localapi.Agent{}, localapi.Errorf(http.StatusConflict,
				"policy %s sends only to %s; send it elsewhere first: sevjala policy set %s --to <node>[,<node>] (or --to all)",
				p.Name, a.Hostname, p.Name)
		}
	}
	only, err := c.Store.OnlyCopies(ctx, a.AgentID)
	if err != nil {
		return localapi.Agent{}, err
	}
	if len(only) > 0 && !force {
		backups := "backups have their"
		if len(only) == 1 {
			backups = "backup has its"
		}
		return localapi.Agent{}, localapi.Errorf(http.StatusConflict,
			"%d %s only copy on %s; they stay on that PC, but this controller would stop tracking them. Remove anyway with: sevjala agents remove %s --force",
			len(only), backups, a.Hostname, agent)
	}
	if err := c.Store.RemoveAgent(ctx, a.AgentID); err != nil {
		return localapi.Agent{}, err
	}
	c.pollMu.Lock()
	delete(c.nodes, a.AgentID)
	c.pollMu.Unlock()
	c.mu.Lock()
	if d, ok := c.seen[a.AgentID]; ok {
		d.Paired = false
		c.seen[a.AgentID] = d
	}
	c.mu.Unlock()
	c.Log.Info("agent removed", logx.Event(logx.EventAgentRemoved), "agent_id", a.AgentID, "hostname", a.Hostname,
		"only_copies", len(only))
	return toAPI(a), nil
}

func (c *Controller) Agents(ctx context.Context) ([]localapi.Agent, error) {
	as, err := c.Store.Agents(ctx)
	if err != nil {
		return nil, err
	}
	polls := c.pollSnapshot()
	out := make([]localapi.Agent, len(as))
	for i, a := range as {
		out[i] = toAPI(a)
		out[i].Problem = polls[a.AgentID].problem
	}
	return out, nil
}

func (c *Controller) Ping(ctx context.Context, agent string) (localapi.Ping, error) {
	as, err := c.Store.Agents(ctx)
	if err != nil {
		return localapi.Ping{}, err
	}
	a, err := resolve(as, agent, func(a store.Agent) (string, string) { return a.AgentID, a.Hostname }, pairedHint)
	if err != nil {
		return localapi.Ping{}, err
	}
	addr := net.JoinHostPort(a.LastIP, strconv.Itoa(a.Port))
	start := time.Now()
	st, err := c.LAN.Status(ctx, addr, a.FP)
	if err != nil {
		return localapi.Ping{}, localapi.Errorf(http.StatusBadGateway, "%s: %v", a.Hostname, err)
	}
	rtt := time.Since(start)
	now := time.Now().UTC().Truncate(time.Second)
	if err := c.Store.MarkSeen(ctx, a.AgentID, st.Version, now); err != nil {
		return localapi.Ping{}, err
	}
	a.Version, a.LastSeen, a.State = st.Version, now, "online"
	return localapi.Ping{Agent: toAPI(a), RTTMs: rtt.Milliseconds(), UptimeS: st.UptimeS, Health: st.Health}, nil
}

func (c *Controller) remember(ds ...localapi.Discovered) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = map[string]localapi.Discovered{}
	}
	for _, d := range ds {
		c.seen[d.AgentID] = d
	}
}

func toAPI(a store.Agent) localapi.Agent {
	return localapi.Agent{AgentID: a.AgentID, Hostname: a.Hostname, Addr: net.JoinHostPort(a.LastIP, strconv.Itoa(a.Port)),
		Role: a.Role, Version: a.Version, FP: a.FP, PairedAt: a.PairedAt, LastSeen: a.LastSeen, State: a.State,
		FreeBytes: a.FreeBytes}
}
