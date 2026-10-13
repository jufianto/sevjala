package coord

import (
	"context"
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/lanapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/store"
)

// offlineAfter missed polls in a row make a node offline (blueprint: three missed polls).
const offlineAfter = 3

// nodeState is what the controller knows of a node from its last polls.
type nodeState struct {
	state, problem string
	misses         int
	polled         bool // the last poll that counted answered: status is current
	polledAt       time.Time
	status         lanapi.Status
}

// PollAll calls /status on every paired node at once and records the result.
// It reports whether a node came back (answered after being offline, in error,
// or unknown since start), which calls for a reconcile.
func (c *Controller) PollAll(ctx context.Context) (cameBack bool) {
	as, err := c.Store.Agents(ctx)
	if err != nil {
		c.Log.Error("poll: read agents", "err", err)
		return false
	}
	timeout := c.PollTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	type result struct {
		a   store.Agent
		st  lanapi.Status
		err error
		at  time.Time
	}
	var mu sync.Mutex
	var results []result
	var wg sync.WaitGroup
	for _, a := range as {
		if a.Role != config.RoleNode {
			continue
		}
		wg.Go(func() {
			at := time.Now()
			pctx, cancel := context.WithTimeout(ctx, timeout)
			st, err := c.LAN.Status(pctx, net.JoinHostPort(a.LastIP, strconv.Itoa(a.Port)), a.FP)
			cancel()
			mu.Lock()
			results = append(results, result{a, st, err, at})
			mu.Unlock()
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return false
	}
	for _, r := range results {
		if c.observe(ctx, r.a, r.st, r.err, r.at) {
			cameBack = true
		}
	}
	return cameBack
}

func (c *Controller) observe(ctx context.Context, a store.Agent, st lanapi.Status, err error, at time.Time) bool {
	c.pollMu.Lock()
	defer c.pollMu.Unlock()
	if c.nodes == nil {
		c.nodes = map[string]*nodeState{}
	}
	n := c.nodes[a.AgentID]
	if n == nil {
		n = &nodeState{}
		c.nodes[a.AgentID] = n
	}
	prev := n.state
	var serr error
	switch {
	case err == nil:
		n.misses, n.polled, n.polledAt, n.status = 0, true, at, st
		n.state, n.problem = store.AgentOnline, ""
		if st.Health != "healthy" {
			n.state, n.problem = store.AgentWarning, strings.Join(st.Problems, "; ")
		}
		var free int64
		if st.Store != nil {
			free = st.Store.FreeBytes
		}
		serr = c.Store.RecordPoll(ctx, a.AgentID, n.state, st.Version, free, at.UTC().Truncate(time.Second))
	case errors.Is(err, lanapi.ErrRejected), errors.Is(err, lanapi.ErrPinMismatch):
		n.misses++
		n.polled, n.state, n.problem = false, store.AgentError, err.Error()
		serr = c.Store.SetAgentState(ctx, a.AgentID, n.state)
	default:
		n.misses++
		n.problem = err.Error()
		if n.misses >= offlineAfter && n.state != store.AgentOffline {
			n.polled, n.state = false, store.AgentOffline
			serr = c.Store.SetAgentState(ctx, a.AgentID, n.state)
		}
	}
	if serr != nil {
		c.Log.Error("poll: record agent state", "agent_id", a.AgentID, "err", serr)
	}
	if n.state == prev {
		return false
	}
	event := map[string]string{store.AgentOnline: logx.EventAgentOnline, store.AgentWarning: logx.EventAgentWarning,
		store.AgentOffline: logx.EventAgentOffline, store.AgentError: logx.EventAgentError}[n.state]
	if event != "" {
		c.Log.Info("agent "+n.state, logx.Event(event), "agent_id", a.AgentID, "hostname", a.Hostname, "reason", n.problem)
	}
	up := n.state == store.AgentOnline || n.state == store.AgentWarning
	return up && (prev == "" || prev == store.AgentOffline || prev == store.AgentError)
}

// pollSnapshot copies the poll cache for reconciliation and views.
func (c *Controller) pollSnapshot() map[string]nodeState {
	c.pollMu.Lock()
	defer c.pollMu.Unlock()
	out := make(map[string]nodeState, len(c.nodes))
	for id, n := range c.nodes {
		out[id] = *n
	}
	return out
}

// forget drops a deleted backup from the node's last inventory, so a reconcile
// before the next poll does not act on it again. The slice is copied: snapshots
// share it.
func (c *Controller) forget(agentID, backupID string) {
	c.pollMu.Lock()
	defer c.pollMu.Unlock()
	if n := c.nodes[agentID]; n != nil && n.status.Backups != nil {
		n.status.Backups = slices.DeleteFunc(slices.Clone(n.status.Backups), func(r lanapi.BackupRef) bool { return r.BackupID == backupID })
	}
}
