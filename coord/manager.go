package coord

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/lanapi"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/manage"
	"github.com/jufianto/sevjala/store"
)

// SetManager lets a paired backup PC run controller commands over manager
// links, or stops it (blueprint AD-21).
func (c *Controller) SetManager(ctx context.Context, agent string, on bool) (localapi.Agent, error) {
	if agent == c.Self.AgentID || strings.EqualFold(agent, c.Host) {
		return localapi.Agent{}, localapi.Errorf(http.StatusConflict, "%s is the controller; only a backup PC can be a manager", c.Host)
	}
	as, err := c.Store.Agents(ctx)
	if err != nil {
		return localapi.Agent{}, err
	}
	a, err := resolve(as, agent, func(a store.Agent) (string, string) { return a.AgentID, a.Hostname }, pairedHint)
	if err != nil {
		return localapi.Agent{}, err
	}
	if a.Role != config.RoleNode {
		return localapi.Agent{}, localapi.Errorf(http.StatusConflict, "%s is the controller; only a backup PC can be a manager", a.Hostname)
	}
	if err := c.Store.SetManager(ctx, a.AgentID, on); err != nil {
		return localapi.Agent{}, err
	}
	a.Manager = on
	c.Log.InfoContext(ctx, "manager set", logx.Event(logx.EventManagerSet), "agent_id", a.AgentID, "hostname", a.Hostname, "manager", on)
	if c.OnManagersChanged != nil {
		c.OnManagersChanged()
	}
	return toAPI(a), nil
}

func (s *Service) SetManager(ctx context.Context, agent string, on bool) (localapi.Agent, error) {
	if s.Controller == nil {
		return localapi.Agent{}, errNotController
	}
	if !localapi.CallerIsAdmin(ctx) {
		return localapi.Agent{}, localapi.AdminRequired(ctx, "making a backup PC a manager")
	}
	return s.Controller.SetManager(ctx, agent, on)
}

// SyncManagers keeps manager links to every manager the last poll found
// online, and drops the others' (blueprint AD-21).
func (c *Controller) SyncManagers(ctx context.Context) {
	if c.Keeper == nil {
		return
	}
	as, err := c.Store.Agents(ctx)
	if err != nil {
		c.Log.Error("manager links: read agents", "err", err)
		return
	}
	polls := c.pollSnapshot()
	var ms []manage.Manager
	for _, a := range as {
		if s := polls[a.AgentID].state; a.Manager && (s == store.AgentOnline || s == store.AgentWarning) {
			ms = append(ms, manage.Manager{AgentID: a.AgentID, Hostname: a.Hostname})
		}
	}
	c.Keeper.Sync(ms)
}

// DialManager opens one manager link to agentID.
func (c *Controller) DialManager(ctx context.Context, agentID string) (net.Conn, error) {
	a, err := c.Store.Agent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return c.LAN.Manage(ctx, net.JoinHostPort(a.LastIP, strconv.Itoa(a.Port)), a.FP)
}

// StillManager is checked on every request a manager link carries.
func (c *Controller) StillManager(agentID string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := c.Store.Agent(ctx, agentID)
	return err == nil && a.Manager
}

// unmanage tells a removed manager it is one no more: no poll will reach it
// again, and without this it would keep offering the controller's dashboard.
func (c *Controller) unmanage(ctx context.Context, a store.Agent) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, _ = c.LAN.PollStatus(ctx, net.JoinHostPort(a.LastIP, strconv.Itoa(a.Port)), a.FP, lanapi.Hello{Host: c.Host})
}
