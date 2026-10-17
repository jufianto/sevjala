package coord

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/scheduler"
	"github.com/jufianto/sevjala/store"
)

const maxKeep = 1000

func (c *Controller) Policies(ctx context.Context) ([]localapi.Policy, error) {
	ps, err := c.Store.Policies(ctx)
	if err != nil {
		return nil, err
	}
	hosts, err := c.hostnames(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]localapi.Policy, len(ps))
	for i, p := range ps {
		out[i] = toPolicy(p, hosts, time.Now())
	}
	return out, nil
}

func (c *Controller) AddPolicy(ctx context.Context, ch localapi.PolicyChange) (localapi.Policy, error) {
	if !tagPattern.MatchString(ch.Name) {
		return localapi.Policy{}, localapi.Errorf(http.StatusBadRequest,
			"policy name %q: use up to 32 lower-case letters, digits, '-' and '_'", ch.Name)
	}
	if ch.Keep == nil {
		return localapi.Policy{}, localapi.Errorf(http.StatusBadRequest, "keep is required: how many backups each node keeps")
	}
	p := store.Policy{Name: ch.Name, Enabled: true, Destinations: []string{}}
	if err := c.applyPolicy(ctx, &p, ch); err != nil {
		return localapi.Policy{}, err
	}
	if _, err := c.Store.Policy(ctx, p.Name); err == nil {
		return localapi.Policy{}, policyExists(p.Name)
	}
	if done, v, err := c.checkDeletes(ctx, p, ch); done {
		return v, err
	}
	if err := c.Store.InsertPolicy(ctx, p); errors.Is(err, store.ErrExists) {
		return localapi.Policy{}, policyExists(p.Name)
	} else if err != nil {
		return localapi.Policy{}, err
	}
	return c.policyChanged(ctx, p, "added")
}

func (c *Controller) SetPolicy(ctx context.Context, name string, ch localapi.PolicyChange) (localapi.Policy, error) {
	p, err := c.policy(ctx, name)
	if err != nil {
		return localapi.Policy{}, err
	}
	if err := c.applyPolicy(ctx, &p, ch); err != nil {
		return localapi.Policy{}, err
	}
	if done, v, err := c.checkDeletes(ctx, p, ch); done {
		return v, err
	}
	if err := c.Store.UpdatePolicy(ctx, p); err != nil {
		return localapi.Policy{}, err
	}
	return c.policyChanged(ctx, p, "changed")
}

// RemovePolicy stops the schedule; backups carrying the tag are kept.
func (c *Controller) RemovePolicy(ctx context.Context, name string) error {
	if name == defaultTag {
		return localapi.Errorf(http.StatusConflict, "the manual policy is used by 'sevjala backup now' and cannot be removed")
	}
	if name == preRestoreTag {
		return localapi.Errorf(http.StatusConflict, "the pre-restore policy keeps the backups made before 'sevjala restore --replace-production' and cannot be removed")
	}
	if _, err := c.policy(ctx, name); err != nil {
		return err
	}
	if err := c.Store.DeletePolicy(ctx, name); err != nil {
		return err
	}
	c.Log.InfoContext(ctx, "policy changed", logx.Event(logx.EventPolicyChanged), "name", name, "what", "removed")
	return nil
}

func (c *Controller) policy(ctx context.Context, name string) (store.Policy, error) {
	p, err := c.Store.Policy(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return p, localapi.Errorf(http.StatusNotFound, "no policy %s (see 'sevjala policy list')", name)
	}
	return p, err
}

func (c *Controller) applyPolicy(ctx context.Context, p *store.Policy, ch localapi.PolicyChange) error {
	if ch.Schedule != nil {
		sched := strings.TrimSpace(*ch.Schedule)
		if _, err := scheduler.Parse(sched); err != nil {
			return localapi.Errorf(http.StatusBadRequest, "%v", err)
		}
		p.Cron = sched
	}
	if ch.Keep != nil {
		if *ch.Keep < 1 || *ch.Keep > maxKeep {
			return localapi.Errorf(http.StatusBadRequest, "keep %d: must be 1-%d", *ch.Keep, maxKeep)
		}
		p.Keep = *ch.Keep
	}
	if ch.To != nil {
		ids, err := c.destinations(ctx, ch.To)
		if err != nil {
			return err
		}
		p.Destinations = ids
	}
	if ch.Enabled != nil {
		p.Enabled = *ch.Enabled
	}
	return nil
}

// destinations resolves agent references to paired node IDs; none or "all" is every node.
func (c *Controller) destinations(ctx context.Context, refs []string) ([]string, error) {
	if len(refs) == 0 || (len(refs) == 1 && refs[0] == "all") {
		return []string{}, nil
	}
	as, err := c.Store.Agents(ctx)
	if err != nil {
		return nil, err
	}
	as = slices.DeleteFunc(as, func(a store.Agent) bool { return a.Role != config.RoleNode })
	ids := []string{}
	for _, ref := range refs {
		a, err := resolve(as, ref, func(a store.Agent) (string, string) { return a.AgentID, a.Hostname }, pairedHint)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(ids, a.AgentID) {
			ids = append(ids, a.AgentID)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

func (c *Controller) policyChanged(ctx context.Context, p store.Policy, what string) (localapi.Policy, error) {
	c.Log.InfoContext(ctx, "policy changed", logx.Event(logx.EventPolicyChanged), "name", p.Name, "what", what,
		"schedule", p.Cron, "keep", p.Keep, "to", p.Destinations, "enabled", p.Enabled)
	hosts, err := c.hostnames(ctx)
	return toPolicy(p, hosts, time.Now()), err
}

func toPolicy(p store.Policy, hosts map[string]string, now time.Time) localapi.Policy {
	out := localapi.Policy{Name: p.Name, Schedule: p.Cron, Keep: p.Keep, Enabled: p.Enabled, To: []localapi.PolicyTarget{}}
	for _, id := range p.Destinations {
		host := hosts[id]
		if host == "" {
			host = id
		}
		out.To = append(out.To, localapi.PolicyTarget{AgentID: id, Hostname: host})
	}
	if s, err := scheduler.Parse(p.Cron); err == nil && p.Enabled {
		if next, ok := s.Next(now); ok {
			out.NextRun = next
		}
	}
	return out
}

func policyExists(name string) error {
	return localapi.Errorf(http.StatusConflict, "policy %s already exists; change it with 'sevjala policy set'", name)
}

// checkDeletes answers a dry run with what p would delete, or refuses an
// unconfirmed change that deletes; done means the caller returns v, err.
func (c *Controller) checkDeletes(ctx context.Context, p store.Policy, ch localapi.PolicyChange) (done bool, v localapi.Policy, err error) {
	dels, err := c.previewDeletes(ctx, p)
	if err != nil {
		return true, v, err
	}
	if ch.DryRun {
		hosts, err := c.hostnames(ctx)
		v = toPolicy(p, hosts, time.Now())
		v.Deletes = dels
		return true, v, err
	}
	if err := confirmDeletes(p.Name, ch.Confirm, dels); err != nil {
		return true, v, err
	}
	return false, v, nil
}
