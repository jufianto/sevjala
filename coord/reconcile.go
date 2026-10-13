package coord

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/retention"
	"github.com/jufianto/sevjala/store"
	"github.com/jufianto/sevjala/transfer"
)

// Reconcile compares what the store expects with what the nodes reported in
// their last poll, then deletes, sends again, prunes staging and expires
// (blueprint: Retention and reconciliation). A dry run only returns the plan.
func (c *Controller) Reconcile(ctx context.Context, dryRun bool) (localapi.RetentionPlan, error) {
	c.reconcileMu.Lock()
	defer c.reconcileMu.Unlock()
	if !dryRun && c.anyOffline() {
		// A node that changed address answers discovery; this heals DHCP (D-015).
		if _, err := c.Discover(ctx); err != nil {
			c.Log.Warn("reconcile: discovery", "err", err)
		}
	}
	in, agents, known, err := c.retentionInput(ctx)
	if err != nil {
		return localapi.RetentionPlan{}, err
	}
	plan := retention.Compute(in)
	view := planView(plan, dryRun, agents)
	if !dryRun {
		c.apply(ctx, plan, agents, known)
	}
	return view, nil
}

func (c *Controller) anyOffline() bool {
	for _, n := range c.pollSnapshot() {
		if n.state == store.AgentOffline {
			return true
		}
	}
	return false
}

func (c *Controller) retentionInput(ctx context.Context) (retention.Input, map[string]store.Agent, map[string]bool, error) {
	// Jobs before the store: runBackup inserts the row before it drops the job,
	// so a backup that just finished is found in one of the two.
	busy := map[string]bool{}
	c.mu.Lock()
	for id := range c.jobs {
		busy[id] = true
	}
	c.mu.Unlock()
	ps, err := c.Store.Policies(ctx)
	if err != nil {
		return retention.Input{}, nil, nil, err
	}
	bs, err := c.Store.Backups(ctx, true)
	if err != nil {
		return retention.Input{}, nil, nil, err
	}
	as, err := c.Store.Agents(ctx)
	if err != nil {
		return retention.Input{}, nil, nil, err
	}
	in := retention.Input{StagingKeep: c.config().StagingKeep(), Busy: busy, Now: time.Now()}
	for _, p := range ps {
		in.Policies = append(in.Policies, retention.Policy{Name: p.Name, Keep: p.Keep, Dest: p.Destinations})
	}
	known := map[string]bool{}
	for _, b := range bs {
		known[b.BackupID] = true
		rb := retention.Backup{ID: b.BackupID, Database: b.Database, SHA256: b.SHA256, Created: b.CreatedAt, Size: b.Size,
			Tags: b.Tags, Expired: b.State == store.BackupExpired, Staged: b.StagingPath != "" && c.inStaging(b.BackupID),
			Copies: map[string]retention.Copy{}}
		for _, cp := range b.Copies {
			rb.Copies[cp.AgentID] = retention.Copy{State: cp.State, VerifiedAt: cp.VerifiedAt}
		}
		in.Backups = append(in.Backups, rb)
	}
	polls := c.pollSnapshot()
	agents := map[string]store.Agent{}
	for _, a := range as {
		if a.Role != config.RoleNode {
			continue
		}
		agents[a.AgentID] = a
		n := retention.Node{AgentID: a.AgentID}
		// A null inventory means the node could not read its store: unknown, not empty.
		if s := polls[a.AgentID]; s.polled && s.status.Backups != nil {
			n.Polled, n.PolledAt, n.Actual = true, s.polledAt, map[string]retention.Held{}
			for _, h := range s.status.Backups {
				n.Actual[h.BackupID] = retention.Held{State: h.State, SHA256: h.SHA256}
			}
			if s.status.Store != nil {
				n.FreeBytes, n.MinFree = s.status.Store.FreeBytes, s.status.Store.MinFreeBytes
			}
		}
		in.Nodes = append(in.Nodes, n)
	}
	return in, agents, known, nil
}

func (c *Controller) inStaging(id string) bool {
	_, err := os.Stat(filepath.Join(c.Staging, id+inventory.DataExt))
	return err == nil
}

func (c *Controller) apply(ctx context.Context, plan retention.Plan, agents map[string]store.Agent, known map[string]bool) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, u := range plan.Updates {
		if err := c.Store.MarkCopy(ctx, u.BackupID, u.AgentID, u.State, u.Reason, now); err != nil {
			c.Log.Error("reconcile: record copy", "backup_id", u.BackupID, "agent_id", u.AgentID, "err", err)
		}
	}
	notDeleted := map[[2]string]bool{}
	for _, d := range plan.Deletes {
		a := agents[d.AgentID]
		addr := net.JoinHostPort(a.LastIP, strconv.Itoa(a.Port))
		was, err := c.LAN.DeleteBackup(ctx, addr, a.FP, d.BackupID, d.Reason)
		if err != nil {
			notDeleted[[2]string{d.BackupID, d.AgentID}] = true
			c.Log.Warn("retention delete failed", "backup_id", d.BackupID, "agent_id", d.AgentID, "err", err)
			continue
		}
		c.forget(d.AgentID, d.BackupID)
		if err := c.Store.MarkCopy(ctx, d.BackupID, d.AgentID, store.CopyDeleted, d.Reason, now); err != nil {
			c.Log.Error("reconcile: record delete", "backup_id", d.BackupID, "agent_id", d.AgentID, "err", err)
		}
		c.Log.Info("retention delete", logx.Event(logx.EventRetentionDelete), "backup_id", d.BackupID,
			"agent_id", d.AgentID, "hostname", a.Hostname, "reason", d.Reason, "was_there", was)
	}
	for _, s := range plan.Sends {
		if notDeleted[[2]string{s.BackupID, s.AgentID}] {
			continue // the corrupt copy is still there; a push would conflict
		}
		if err := c.Store.EnsureCopy(ctx, s.BackupID, s.AgentID); err != nil {
			c.Log.Error("reconcile: plan copy", "backup_id", s.BackupID, "agent_id", s.AgentID, "err", err)
			continue
		}
		c.Pool.Enqueue(transfer.Job{BackupID: s.BackupID, AgentID: s.AgentID})
		c.Log.Info("backfill queued", logx.Event(logx.EventBackfillQueued), "backup_id", s.BackupID,
			"agent_id", s.AgentID, "reason", s.Reason)
	}
	for _, id := range plan.Unstage {
		if err := c.unstage(id); err != nil {
			c.Log.Error("reconcile: prune staging", "backup_id", id, "err", err)
			continue
		}
		if err := c.Store.Unstage(ctx, id); err != nil {
			c.Log.Error("reconcile: record staging", "backup_id", id, "err", err)
		}
		c.Log.Info("staging pruned", logx.Event(logx.EventStagingPruned), "backup_id", id, "reason", "retention")
	}
	c.pruneOrphans(ctx, known)
	for _, id := range plan.Expire {
		if err := c.Store.Expire(ctx, id); err != nil {
			c.Log.Error("reconcile: expire", "backup_id", id, "err", err)
			continue
		}
		c.Log.Info("backup expired", "backup_id", id)
	}
	for _, k := range plan.Kept {
		key := [2]string{k.BackupID, k.AgentID}
		if k.Reason == retention.ReasonUnknown && !c.unknownSeen[key] {
			if c.unknownSeen == nil {
				c.unknownSeen = map[[2]string]bool{}
			}
			c.unknownSeen[key] = true
			c.Log.Warn("unknown backup kept", logx.Event(logx.EventUnknownBackup), "backup_id", k.BackupID, "agent_id", k.AgentID)
		}
	}
}

// unstage removes the data file, then the manifest.
func (c *Controller) unstage(id string) error {
	for _, ext := range []string{inventory.DataExt, inventory.ManifestExt} {
		if err := os.Remove(filepath.Join(c.Staging, id+ext)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// pruneOrphans removes staging files of backups the store does not know and
// no dump holds, such as one finished as the agent stopped. known is from the
// reconcile's snapshot, so a candidate is checked again: a dump may have
// finished since (it adds the row before it leaves the jobs).
func (c *Controller) pruneOrphans(ctx context.Context, known map[string]bool) {
	es, err := os.ReadDir(c.Staging)
	if err != nil {
		c.Log.Error("reconcile: read staging", "err", err)
		return
	}
	for _, e := range es {
		id, ok := strings.CutSuffix(e.Name(), inventory.DataExt)
		if !ok {
			id, ok = strings.CutSuffix(e.Name(), inventory.ManifestExt)
		}
		if !ok || !inventory.ValidID(id) || known[id] || c.busy(id) {
			continue
		}
		if _, err := c.Store.Backup(ctx, id); !errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err := os.Remove(filepath.Join(c.Staging, e.Name())); err != nil {
			c.Log.Error("reconcile: prune staging", "file", e.Name(), "err", err)
			continue
		}
		c.Log.Info("staging pruned", logx.Event(logx.EventStagingPruned), "file", e.Name(), "reason", "no backup record")
	}
}

func planView(p retention.Plan, dryRun bool, agents map[string]store.Agent) localapi.RetentionPlan {
	conv := func(as []retention.Action) []localapi.RetentionAction {
		out := make([]localapi.RetentionAction, len(as))
		for i, a := range as {
			host := agents[a.AgentID].Hostname
			if host == "" {
				host = a.AgentID
			}
			out[i] = localapi.RetentionAction{BackupID: a.BackupID, AgentID: a.AgentID, Hostname: host, Reason: a.Reason}
		}
		return out
	}
	nonNil := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	return localapi.RetentionPlan{DryRun: dryRun, Deletes: conv(p.Deletes), Sends: conv(p.Sends), Kept: conv(p.Kept),
		Unstaged: nonNil(p.Unstage), Expired: nonNil(p.Expire)}
}

func (c *Controller) busy(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.jobs[id]
	return ok
}
