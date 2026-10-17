package coord

import (
	"context"
	"time"

	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/scheduler"
)

// failedRetry is how long the scheduler waits after a failed dump of a
// database before trying it again.
const failedRetry = 15 * time.Minute

// Tick starts, per configured database, one backup tagged with every policy
// that is due at now (blueprint AD-09). A backup created after now (the clock
// moved back) is ignored, so the schedule neither stalls nor repeats.
func (c *Controller) Tick(ctx context.Context, now time.Time) {
	cfg := c.config()
	dbs := cfg.Controller.Database.Databases
	if len(dbs) == 0 {
		return
	}
	ps, err := c.Store.Policies(ctx)
	if err != nil {
		c.Log.Error("schedule: read policies", "err", err)
		return
	}
	sched := make([]scheduler.Policy, 0, len(ps))
	for _, p := range ps {
		s, err := scheduler.Parse(p.Cron)
		if err != nil {
			c.Log.Error("schedule: bad policy", "policy", p.Name, "err", err)
			continue
		}
		sched = append(sched, scheduler.Policy{Name: p.Name, Schedule: s, Enabled: p.Enabled})
	}
	bs, err := c.Store.Backups(ctx, true)
	if err != nil {
		c.Log.Error("schedule: read backups", "err", err)
		return
	}
	newest := map[string]map[string]time.Time{}
	for _, b := range bs {
		if b.CreatedAt.After(now) {
			continue
		}
		if newest[b.Database] == nil {
			newest[b.Database] = map[string]time.Time{}
		}
		for _, t := range b.Tags {
			if b.CreatedAt.After(newest[b.Database][t]) {
				newest[b.Database][t] = b.CreatedAt
			}
		}
	}
	for _, db := range dbs {
		tags := scheduler.Due(now, sched, newest[db])
		if len(tags) == 0 {
			continue
		}
		c.mu.Lock()
		_, busy := c.running[db]
		failed := c.failedAt[db]
		c.mu.Unlock()
		if busy || (!failed.IsZero() && now.Sub(failed) < failedRetry && !failed.After(now)) {
			continue
		}
		jobs, err := c.start([]string{db}, tags, now, cfg, "")
		if err != nil {
			c.Log.Warn("schedule: backup not started", "database", db, "tags", tags, "err", err)
			continue
		}
		c.Log.Info("schedule fired", logx.Event(logx.EventScheduleFired), "database", db, "tags", tags,
			"backup_id", jobs[0].BackupID)
	}
}
