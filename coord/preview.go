package coord

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/retention"
	"github.com/jufianto/sevjala/store"
)

// previewDeletes is what retention would delete once next (a policy added or
// changed) is saved, beyond what it deletes already. A PC not polled counts as
// holding the copies the store has verified there, so a change made while it
// is offline is still confirmed.
func (c *Controller) previewDeletes(ctx context.Context, next store.Policy) ([]localapi.RetentionAction, error) {
	in, agents, _, err := c.retentionInput(ctx)
	if err != nil {
		return nil, err
	}
	for i := range in.Nodes {
		n := &in.Nodes[i]
		if n.Polled {
			continue
		}
		n.Polled, n.PolledAt, n.Actual = true, in.Now, map[string]retention.Held{}
		for _, b := range in.Backups {
			if b.Copies[n.AgentID].State == store.CopyVerified {
				n.Actual[b.ID] = retention.Held{State: inventory.StateVerified, SHA256: b.SHA256}
			}
		}
	}
	before := retention.Compute(in)
	rp := retention.Policy{Name: next.Name, Keep: next.Keep, Dest: next.Destinations}
	ps := slices.DeleteFunc(slices.Clone(in.Policies), func(p retention.Policy) bool { return p.Name == next.Name })
	in.Policies = append(ps, rp)
	after := retention.Compute(in)
	var extra retention.Plan
	for _, d := range after.Deletes {
		if !slices.Contains(before.Deletes, d) {
			extra.Deletes = append(extra.Deletes, d)
		}
	}
	return planView(extra, true, agents).Deletes, nil
}

// confirmDeletes refuses a policy change that deletes backups until the caller
// repeats the policy name.
func confirmDeletes(name, confirm string, deletes []localapi.RetentionAction) error {
	if len(deletes) == 0 || confirm == name {
		return nil
	}
	var hosts []string
	for _, d := range deletes {
		if !slices.Contains(hosts, d.Hostname) {
			hosts = append(hosts, d.Hostname)
		}
	}
	slices.Sort(hosts)
	what := fmt.Sprintf("%d backups", len(deletes))
	if len(deletes) == 1 {
		what = "1 backup"
	}
	return localapi.ConfirmRequired(fmt.Sprintf("this change to policy %s deletes %s on %s", name, what, strings.Join(hosts, ", ")),
		name, len(deletes), hosts)
}
