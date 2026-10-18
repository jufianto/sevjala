package coord

import (
	"cmp"
	"context"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/store"
)

const maxName = 40

// SetName gives a backup PC the name every screen and command shows for it;
// "" goes back to the name the PC reports. Names are typed in commands, so
// no two PCs may answer to the same one.
func (c *Controller) SetName(ctx context.Context, agent, name string) (localapi.Agent, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n > maxName {
		return localapi.Agent{}, localapi.Errorf(http.StatusBadRequest, "a name is at most %d characters; %q has %d", maxName, name, n)
	}
	if strings.ContainsFunc(name, func(r rune) bool { return !unicode.IsGraphic(r) }) {
		return localapi.Agent{}, localapi.Errorf(http.StatusBadRequest, "a name is letters, digits, spaces and punctuation; %q has others", name)
	}
	if agent == c.Self.AgentID || strings.EqualFold(agent, c.Host) {
		return localapi.Agent{}, localapi.Errorf(http.StatusConflict, "%s is the controller; only a backup PC can be renamed", c.Host)
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
		return localapi.Agent{}, localapi.Errorf(http.StatusConflict, "%s is the controller; only a backup PC can be renamed", a.Hostname)
	}
	if strings.EqualFold(name, c.Host) {
		return localapi.Agent{}, localapi.Errorf(http.StatusConflict, "%q is already the name of %s", name, c.Host)
	}
	for _, o := range as {
		if o.AgentID == a.AgentID || name == "" {
			continue
		}
		if strings.EqualFold(name, o.Hostname) {
			return localapi.Agent{}, localapi.Errorf(http.StatusConflict, "%q is already the name of %s", name, o.Hostname)
		}
		if strings.EqualFold(name, o.ComputerName) {
			return localapi.Agent{}, localapi.Errorf(http.StatusConflict, "%q is the computer name of %s", name, o.Hostname)
		}
	}
	if name == a.ComputerName {
		name = ""
	}
	if err := c.Store.SetName(ctx, a.AgentID, name); err != nil {
		return localapi.Agent{}, err
	}
	c.Log.InfoContext(ctx, "agent renamed", logx.Event(logx.EventAgentRenamed), "agent_id", a.AgentID,
		"hostname", a.ComputerName, "name", name)
	a.Hostname = cmp.Or(name, a.ComputerName)
	return toAPI(a), nil
}

func (s *Service) SetName(ctx context.Context, agent, name string) (localapi.Agent, error) {
	if s.Controller == nil {
		return localapi.Agent{}, errNotController
	}
	return s.Controller.SetName(ctx, agent, name)
}
