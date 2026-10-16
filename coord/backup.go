package coord

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/dump"
	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/lanapi"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/store"
	"github.com/jufianto/sevjala/transfer"
)

const defaultTag = "manual"

var tagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// job is a dump in progress or a failed one; it leaves memory once the
// backup is in the store.
type job struct {
	id, db string
	bytes  atomic.Int64
	err    string // guarded by Controller.mu
}

func (c *Controller) BackupNow(ctx context.Context, db string, tags []string) ([]localapi.BackupJob, error) {
	cfg := c.config()
	dbs := cfg.Controller.Database.Databases
	if len(dbs) == 0 {
		return nil, localapi.Errorf(http.StatusBadRequest, "no database is configured; run 'sevjala db detect', then 'sevjala db set --db <name>'")
	}
	if db != "" {
		if !slices.Contains(dbs, db) {
			return nil, localapi.Errorf(http.StatusBadRequest, "%s is not a configured database (configured: %s)", db, strings.Join(dbs, ", "))
		}
		dbs = []string{db}
	}
	if len(tags) == 0 {
		tags = []string{defaultTag}
	}
	ps, err := c.Store.Policies(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Name
	}
	for _, t := range tags {
		if !slices.Contains(names, t) {
			return nil, localapi.Errorf(http.StatusBadRequest, "tag %q is not a policy (policies: %s)", t, strings.Join(names, ", "))
		}
	}
	return c.start(dbs, tags, time.Now(), cfg)
}

// start registers one dump per database, all created at now, and runs them in
// sequence on the agent's lifetime context.
func (c *Controller) start(dbs, tags []string, now time.Time, cfg config.Config) ([]localapi.BackupJob, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, name := range dbs {
		if id, busy := c.running[name]; busy {
			return nil, localapi.Errorf(http.StatusConflict, "a backup of %s is already running (%s)", name, id)
		}
		if c.replacing[name] {
			return nil, localapi.Errorf(http.StatusConflict, "%s is being replaced by a restore; its backups resume when the restore ends", name)
		}
	}
	if c.jobs == nil {
		c.jobs, c.running, c.failedAt = map[string]*job{}, map[string]string{}, map[string]time.Time{}
	}
	out := make([]localapi.BackupJob, len(dbs))
	queued := make([]*job, len(dbs))
	for i, name := range dbs {
		for id, old := range c.jobs {
			if old.db == name && old.err != "" {
				delete(c.jobs, id) // the new attempt replaces the last failure
			}
		}
		id := inventory.NewID(now)
		for c.jobs[id] != nil || c.staged(id) {
			id = inventory.NewID(now)
		}
		j := &job{id: id, db: name}
		c.jobs[id], c.running[name] = j, id
		out[i], queued[i] = localapi.BackupJob{BackupID: id, Database: name}, j
	}
	go func() {
		for _, j := range queued {
			c.runBackup(j, now, tags, cfg)
		}
	}()
	return out, nil
}

// staged reports whether a finished backup already uses id; an id drawn twice
// in the same second must not reach dump.Run.
func (c *Controller) staged(id string) bool {
	_, err := os.Lstat(filepath.Join(c.Staging, id+inventory.DataExt))
	return err == nil
}

func (c *Controller) runBackup(j *job, created time.Time, tags []string, cfg config.Config) {
	defer func() {
		c.mu.Lock()
		delete(c.running, j.db)
		c.mu.Unlock()
	}()
	log := c.Log.With("backup_id", j.id, "database", j.db)
	log.Info("backup started", logx.Event(logx.EventBackupStarted))
	m, err := c.dump(j, created, tags, cfg)
	var nodes []string
	if err == nil {
		nodes, err = c.targets(c.Ctx, tags)
	}
	if err == nil {
		err = c.Store.InsertBackup(c.Ctx, store.Backup{BackupID: m.BackupID, Database: m.Database, CreatedAt: m.CreatedAt,
			Size: m.Size, SHA256: m.SHA256, Tags: m.Tags, StagingPath: m.BackupID + inventory.DataExt}, nodes)
	}
	c.mu.Lock()
	if err != nil {
		j.err = err.Error()
		c.failedAt[j.db] = created
	} else {
		delete(c.jobs, j.id)
		delete(c.failedAt, j.db)
	}
	c.mu.Unlock()
	if err != nil {
		log.Error("backup failed", logx.Event(logx.EventBackupFailed), "err", err)
		return
	}
	log.Info("backup created", logx.Event(logx.EventBackupCreated), "size", m.Size, "sha256", m.SHA256, "nodes", len(nodes))
	for _, n := range nodes {
		c.Pool.Enqueue(transfer.Job{BackupID: m.BackupID, AgentID: n})
	}
}

func (c *Controller) dump(j *job, created time.Time, tags []string, cfg config.Config) (inventory.Manifest, error) {
	tools, conn, err := c.mysql(cfg)
	if err != nil {
		return inventory.Manifest{}, err
	}
	return dump.Run(c.Ctx, dump.Options{ID: j.id, Database: j.db, StagingDir: c.Staging, SourceAgent: c.Self.AgentID,
		Created: created, Tags: tags, Tools: tools, Conn: conn,
		Progress: func(n int64) { j.bytes.Store(n) }})
}

// targets is the union of the destinations of the backup's policies (blueprint:
// Policies and tiers); a policy without destinations, or one removed since
// the dump started, means every paired node.
func (c *Controller) targets(ctx context.Context, tags []string) ([]string, error) {
	as, err := c.Store.Agents(ctx)
	if err != nil {
		return nil, err
	}
	ps, err := c.Store.Policies(ctx)
	if err != nil {
		return nil, err
	}
	all, want := false, map[string]bool{}
	for _, t := range tags {
		i := slices.IndexFunc(ps, func(p store.Policy) bool { return p.Name == t })
		if i < 0 || len(ps[i].Destinations) == 0 {
			all = true
			break
		}
		for _, d := range ps[i].Destinations {
			want[d] = true
		}
	}
	var out []string
	for _, a := range as {
		if a.Role == config.RoleNode && (all || want[a.AgentID]) {
			out = append(out, a.AgentID)
		}
	}
	return out, nil
}

// Push sends one staged backup to one node; the transfer pool calls it and
// retries what it returns unless it is marked Permanent.
func (c *Controller) Push(ctx context.Context, j transfer.Job) error {
	a, err := c.Store.Agent(ctx, j.AgentID)
	if err != nil {
		return transfer.Permanent(err)
	}
	m, err := inventory.ReadManifest(filepath.Join(c.Staging, j.BackupID+inventory.ManifestExt))
	if err != nil {
		return transfer.Permanent(fmt.Errorf("staging copy: %w", err))
	}
	f, err := os.Open(filepath.Join(c.Staging, j.BackupID+inventory.DataExt))
	if err != nil {
		return transfer.Permanent(fmt.Errorf("staging copy: %w", err))
	}
	defer f.Close()
	addr := net.JoinHostPort(a.LastIP, strconv.Itoa(a.Port))
	c.Log.Info("transfer started", logx.Event(logx.EventTransferStarted), "backup_id", j.BackupID, "agent_id", j.AgentID, "addr", addr)
	if err := c.LAN.PutBackup(ctx, addr, a.FP, m, f); err != nil {
		return classify(err)
	}
	return nil
}

// classify tells the pool what a retry can fix. A full disk is not retried on
// the backoff timer: reconciliation sends the copy again once the node reports
// enough free space.
func classify(err error) error {
	var he *lanapi.HTTPError
	var op *net.OpError
	switch {
	case errors.Is(err, lanapi.ErrRejected), errors.Is(err, lanapi.ErrPinMismatch):
		return transfer.Permanent(err)
	case errors.As(err, &he):
		switch he.Code {
		case http.StatusBadRequest, http.StatusForbidden, http.StatusConflict, http.StatusInsufficientStorage:
			return transfer.Permanent(err)
		}
	case errors.As(err, &op) && op.Op == "dial":
		return transfer.Unreachable(err)
	}
	return err
}

// Settle records what the transfer pool reports.
func (c *Controller) Settle(j transfer.Job, u transfer.Update) {
	cp := store.Copy{BackupID: j.BackupID, AgentID: j.AgentID, State: u.State, Attempts: u.Attempts}
	log := c.Log.With("backup_id", j.BackupID, "agent_id", j.AgentID, "attempt", u.Attempts)
	switch {
	case u.State == store.CopyVerified:
		cp.VerifiedAt = time.Now().UTC().Truncate(time.Second)
		log.Info("transfer verified", logx.Event(logx.EventTransferVerified))
	case u.Err != nil:
		cp.LastError = u.Err.Error()
		attrs := []any{logx.Event(logx.EventTransferFailed), "state", u.State, "err", u.Err}
		if u.Retry > 0 {
			attrs = append(attrs, "next_retry", u.Retry.String())
		}
		log.Warn("transfer failed", attrs...)
	}
	if err := c.Store.SetCopy(context.Background(), cp); err != nil {
		c.Log.Error("record transfer state", "backup_id", j.BackupID, "agent_id", j.AgentID, "err", err)
	}
}

// Resume requeues the copies a restart interrupted.
func (c *Controller) Resume(ctx context.Context) error {
	cs, err := c.Store.Unsettled(ctx)
	if err != nil {
		return err
	}
	for _, cp := range cs {
		c.Pool.Enqueue(transfer.Job{BackupID: cp.BackupID, AgentID: cp.AgentID, Attempts: cp.Attempts})
	}
	return nil
}

// Backups merges the store with dumps still in memory; all includes expired backups.
func (c *Controller) Backups(ctx context.Context, all bool) ([]localapi.Backup, error) {
	c.mu.Lock()
	jobs := make([]localapi.Backup, 0, len(c.jobs))
	for _, j := range c.jobs {
		jobs = append(jobs, j.view())
	}
	c.mu.Unlock()
	bs, err := c.Store.Backups(ctx, all)
	if err != nil {
		return nil, err
	}
	hosts, err := c.hostnames(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]localapi.Backup, 0, len(bs))
	stored := map[string]bool{}
	for _, b := range bs {
		out = append(out, toBackup(b, hosts))
		stored[b.BackupID] = true
	}
	for _, j := range jobs {
		if !stored[j.BackupID] {
			out = append(out, j)
		}
	}
	slices.SortFunc(out, func(a, b localapi.Backup) int { return strings.Compare(b.BackupID, a.BackupID) })
	return out, nil
}

// Backup checks the jobs before the store: runBackup commits the row before it
// drops the job, so a landing backup is always found in one of them.
func (c *Controller) Backup(ctx context.Context, id string) (localapi.Backup, error) {
	c.mu.Lock()
	j := c.jobs[id]
	var view localapi.Backup
	if j != nil {
		view = j.view()
	}
	c.mu.Unlock()
	if j != nil {
		return view, nil
	}
	b, err := c.Store.Backup(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return localapi.Backup{}, localapi.Errorf(http.StatusNotFound, "no backup %s", id)
	}
	if err != nil {
		return localapi.Backup{}, err
	}
	hosts, err := c.hostnames(ctx)
	return toBackup(b, hosts), err
}

// view must be called with Controller.mu held.
func (j *job) view() localapi.Backup {
	b := localapi.Backup{BackupID: j.id, Database: j.db, State: localapi.BackupDumping, Bytes: j.bytes.Load(),
		Tags: []string{}, Copies: []localapi.BackupCopy{}}
	if j.err != "" {
		b.State, b.Error = localapi.BackupFailed, j.err
	}
	return b
}

func (c *Controller) hostnames(ctx context.Context) (map[string]string, error) {
	as, err := c.Store.Agents(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(as))
	for _, a := range as {
		out[a.AgentID] = a.Hostname
	}
	return out, nil
}

func toBackup(b store.Backup, hosts map[string]string) localapi.Backup {
	out := localapi.Backup{BackupID: b.BackupID, Database: b.Database, CreatedAt: b.CreatedAt, Size: b.Size, SHA256: b.SHA256,
		Tags: b.Tags, State: b.State, Copies: make([]localapi.BackupCopy, len(b.Copies))}
	for i, cp := range b.Copies {
		host := hosts[cp.AgentID]
		if host == "" {
			host = cp.AgentID
		}
		out.Copies[i] = localapi.BackupCopy{AgentID: cp.AgentID, Hostname: host, State: cp.State, Attempts: cp.Attempts,
			LastError: cp.LastError, VerifiedAt: cp.VerifiedAt}
	}
	return out
}
