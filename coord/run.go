package coord

import (
	"context"
	"time"
)

// Run drives the unattended loops until ctx ends: the scheduler at every
// minute boundary, a /status poll of every node, and reconciliation, also
// right after a node comes back.
func (c *Controller) Run(ctx context.Context) {
	poll := time.NewTicker(c.PollEvery)
	defer poll.Stop()
	rec := time.NewTicker(c.ReconcileEvery)
	defer rec.Stop()
	tick := time.NewTimer(0)
	defer tick.Stop()
	c.pollRound(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			c.Tick(ctx, now)
			tick.Reset(time.Until(now.Truncate(time.Minute).Add(time.Minute)))
		case <-poll.C:
			c.pollRound(ctx)
		case <-rec.C:
			c.reconcile(ctx)
		}
	}
}

func (c *Controller) pollRound(ctx context.Context) {
	if c.PollAll(ctx) {
		c.reconcile(ctx)
	}
}

func (c *Controller) reconcile(ctx context.Context) {
	if _, err := c.Reconcile(ctx, false); err != nil && ctx.Err() == nil {
		c.Log.Error("reconcile failed", "err", err)
	}
}
