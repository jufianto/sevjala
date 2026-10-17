package coord

import (
	"context"
	"slices"

	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/store"
)

// The few actions that need an administrator (AD-20): those that let data
// leave for a PC nobody approved, run a program of the caller's choice as the
// service, read a file as the service, or make retention delete backups.

// tagInUse reports whether live backups carry tag: a new policy for it would
// start deleting them down to its keep.
func (c *Controller) tagInUse(ctx context.Context, tag string) (bool, error) {
	bs, err := c.Store.Backups(ctx, false)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(bs, func(b store.Backup) bool { return slices.Contains(b.Tags, tag) }), nil
}

// policyDeletes reports whether ch makes retention delete copies: keep goes
// down, or a backup PC leaves the policy's destinations.
func (c *Controller) policyDeletes(ctx context.Context, name string, ch localapi.PolicyChange) (bool, error) {
	p, err := c.policy(ctx, name)
	if err != nil {
		return false, err
	}
	if ch.Keep != nil && *ch.Keep < p.Keep {
		return true, nil
	}
	if ch.To == nil {
		return false, nil
	}
	next, err := c.destinations(ctx, ch.To)
	if err != nil {
		return false, err
	}
	if len(next) == 0 {
		return false, nil
	}
	if len(p.Destinations) == 0 {
		return true, nil
	}
	return slices.ContainsFunc(p.Destinations, func(id string) bool { return !slices.Contains(next, id) }), nil
}
