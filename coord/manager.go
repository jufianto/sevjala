package coord

import (
	"context"
	"net/http"
	"strings"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
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
