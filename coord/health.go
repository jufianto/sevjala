package coord

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/scheduler"
	"github.com/jufianto/sevjala/store"
)

const (
	stuckAfter     = 30 * time.Minute // a copy still missing this long on an answering PC is worth a look
	silentAfter    = 15 * time.Minute // a node not polled this long has lost its controller
	minOverdue     = time.Hour
	minStagingFree = 1 << 30
)

type failure struct {
	at  time.Time
	err string
}

// healthInput is everything the controller's verdict is computed from.
type healthInput struct {
	now        time.Time
	databases  []string
	agents     []localapi.Agent // paired nodes
	backups    []store.Backup   // not expired
	staged     func(id string) bool
	failed     map[string]failure // database → its last dump, which failed
	policies   []store.Policy
	incomplete map[string]string // database → the backup that puts it back
	free       int64             // staging; negative when unknown
}

// Health is the controller's verdict for the local /status. It reads only the
// store and memory, never MySQL: the tray asks every 15 s.
func (c *Controller) Health(ctx context.Context) localapi.Health {
	cfg := c.config()
	in := healthInput{now: time.Now(), databases: cfg.Controller.Database.Databases, staged: c.inStaging,
		failed: map[string]failure{}, incomplete: map[string]string{}, free: -1}
	var errs []error
	var err error
	if in.agents, err = c.Agents(ctx); err != nil {
		errs = append(errs, err)
	}
	if in.backups, err = c.Store.Backups(ctx, false); err != nil {
		errs = append(errs, err)
	}
	if in.policies, err = c.Store.Policies(ctx); err != nil {
		errs = append(errs, err)
	}
	if free, err := inventory.FreeBytes(c.Staging); err == nil {
		in.free = free
	}
	c.mu.Lock()
	for _, j := range c.jobs {
		if j.err != "" {
			in.failed[j.db] = failure{at: c.failedAt[j.db], err: j.err}
		}
	}
	c.mu.Unlock()
	for _, db := range in.databases {
		if id := c.incompleteReplace(db); id != "" {
			in.incomplete[db] = id
		}
	}
	h := controllerHealth(in)
	for _, err := range errs {
		h.Items = append([]localapi.HealthItem{{Level: localapi.HealthProblem, Text: "Cannot read the controller's records: " + err.Error()}}, h.Items...)
	}
	return finish(h, h.Headline)
}

func controllerHealth(in healthInput) localapi.Health {
	h := localapi.Health{Items: []localapi.HealthItem{}, FreeBytes: max(in.free, 0)}
	problem := func(format string, a ...any) {
		h.Items = append(h.Items, localapi.HealthItem{Level: localapi.HealthProblem, Text: fmt.Sprintf(format, a...)})
	}
	warn := func(format string, a ...any) {
		h.Items = append(h.Items, localapi.HealthItem{Level: localapi.HealthWarning, Text: fmt.Sprintf(format, a...)})
	}
	hosts := map[string]string{}
	online := map[string]bool{}
	for _, a := range in.agents {
		hosts[a.AgentID] = a.Hostname
		online[a.AgentID] = a.State == store.AgentOnline || a.State == store.AgentWarning
	}

	if len(in.agents) == 0 {
		problem("Not protected: no backup PC is paired; backups exist only on this PC")
	}
	_, scheduled := overdueAt(in.policies, in.now)
	var largest int64
	var oldestGood *localapi.DBHealth
	for _, db := range in.databases {
		d := localapi.DBHealth{Name: db}
		var newest, good *store.Backup
		for i := range in.backups {
			b := &in.backups[i]
			if b.Database != db {
				continue
			}
			if newest == nil || b.CreatedAt.After(newest.CreatedAt) {
				newest = b
			}
			if verifiedCopies(*b) > 0 && (good == nil || b.CreatedAt.After(good.CreatedAt)) {
				good = b
			}
		}
		if f, ok := in.failed[db]; ok {
			d.LastFailedAt, d.LastError = f.at, f.err
			problem("The last backup of %s failed at %s: %s", db, localapi.Clock(f.at, in.now), f.err)
		}
		if id, ok := in.incomplete[db]; ok {
			problem("%s was left partly loaded by a failed restore; run: sevjala restore %s --replace-production", db, id)
		}
		switch {
		case good == nil && len(in.agents) > 0:
			problem("No backup of %s has reached a backup PC yet", db)
		case good != nil && overdue(in.policies, good.CreatedAt, in.now):
			problem("No backup of %s has reached a backup PC since %s", db, localapi.When(good.CreatedAt, in.now))
		}
		if good != nil {
			d.LastGood, d.LastGoodAt, d.Size = good.BackupID, good.CreatedAt, good.Size
			d.Copies, d.Planned = verifiedCopies(*good), plannedCopies(*good)
		}
		if newest != nil {
			largest = max(largest, newest.Size)
			if newest.State == store.BackupPartial && in.now.Sub(newest.CreatedAt) > stuckAfter {
				for _, cp := range newest.Copies {
					if cp.State != store.CopyVerified && cp.State != store.CopyDeleted && online[cp.AgentID] {
						warn("The newest backup of %s is not on %s yet: %s", db, hosts[cp.AgentID], cmp.Or(cp.LastError, cp.State))
					}
				}
			}
		}
		h.Databases = append(h.Databases, d)
	}
	for i := range h.Databases {
		if d := &h.Databases[i]; d.LastGood != "" && (oldestGood == nil || d.LastGoodAt.Before(oldestGood.LastGoodAt)) {
			oldestGood = d
		}
	}
	if in.free >= 0 && in.free < max(minStagingFree, 2*largest) {
		problem("Disk almost full on this PC: %s left", inventory.HumanBytes(in.free))
	}

	waiting, damaged := map[string]int{}, map[string]int{}
	for _, b := range in.backups {
		for _, cp := range b.Copies {
			switch cp.State {
			case store.CopyPending, store.CopyTransferring, store.CopyOffline, store.CopyFailed:
				waiting[cp.AgentID]++
			case store.CopyCorrupt:
				if !in.staged(b.BackupID) {
					damaged[cp.AgentID]++
				}
			}
		}
	}
	for _, a := range in.agents {
		h.Agents = append(h.Agents, localapi.AgentHealth{AgentID: a.AgentID, Hostname: a.Hostname, State: a.State, LastSeen: a.LastSeen, Waiting: waiting[a.AgentID]})
		switch a.State {
		case store.AgentOffline, store.AgentError:
			text := a.Hostname + " has not answered yet"
			if !a.LastSeen.IsZero() {
				text = fmt.Sprintf("%s has not answered since %s", a.Hostname, localapi.When(a.LastSeen, in.now))
			}
			if a.State == store.AgentError && a.Problem != "" {
				text += " (" + a.Problem + ")"
			}
			switch n := waiting[a.AgentID]; {
			case n == 1:
				text += "; 1 copy waits for it"
			case n > 1:
				text += fmt.Sprintf("; %d copies wait for it", n)
			}
			warn("%s", text)
		case store.AgentWarning:
			warn("%s: %s", a.Hostname, cmp.Or(a.Problem, "it reports a problem"))
		}
		if n := damaged[a.AgentID]; n > 0 {
			warn("%s holds %s that cannot be replaced (this PC no longer has that backup)", a.Hostname,
				plural(n, "damaged copy", "damaged copies"))
		}
	}
	if !scheduled {
		warn("No schedule is on: backups run only with 'sevjala backup now'")
	}
	if len(in.databases) == 0 {
		warn("No database chosen: run sevjala db set")
	}
	for _, p := range in.policies {
		if !p.Enabled {
			continue
		}
		if s, err := scheduler.Parse(p.Cron); err == nil {
			if next, ok := s.Next(in.now); ok && (h.NextBackupAt.IsZero() || next.Before(h.NextBackupAt)) {
				h.NextBackupAt, h.NextPolicy = next, p.Name
			}
		}
	}

	headline := ""
	if oldestGood != nil {
		headline = fmt.Sprintf("Protected: last backup %s is on %s", localapi.When(oldestGood.LastGoodAt, in.now),
			plural(oldestGood.Copies, "backup PC", "backup PCs"))
	}
	return finish(h, headline)
}

// overdueAt is when a backup made at last is overdue: once two runs of an
// enabled schedule have passed since it, and at least an hour. It is false
// when no schedule ever fires.
func overdueAt(ps []store.Policy, last time.Time) (time.Time, bool) {
	var at time.Time
	for _, p := range ps {
		if !p.Enabled || p.Cron == "" {
			continue
		}
		s, err := scheduler.Parse(p.Cron)
		if err != nil {
			continue
		}
		a, ok1 := s.Next(last)
		b, ok2 := s.Next(a)
		if !ok1 || !ok2 {
			continue
		}
		if b.Before(last.Add(minOverdue)) {
			b = last.Add(minOverdue)
		}
		if at.IsZero() || b.Before(at) {
			at = b
		}
	}
	return at, !at.IsZero()
}

func overdue(ps []store.Policy, last, now time.Time) bool {
	at, ok := overdueAt(ps, last)
	return ok && now.After(at)
}

type nodeInput struct {
	now, started, contact time.Time
	paired                bool
	store                 *localapi.StoreInfo
	minFree               int64
}

// NodeHealth is a node's verdict: what it holds and whether its controller
// still calls.
func NodeHealth(st *localapi.StoreInfo, minFree int64, paired bool, started, contact time.Time) localapi.Health {
	return nodeHealth(nodeInput{now: time.Now(), started: started, contact: contact, paired: paired, store: st, minFree: minFree})
}

func nodeHealth(in nodeInput) localapi.Health {
	st := in.store
	if st == nil {
		st = &localapi.StoreInfo{}
	}
	h := localapi.Health{Items: []localapi.HealthItem{}, FreeBytes: st.FreeBytes, ControllerContactAt: in.contact}
	if st.FreeBytes < in.minFree {
		h.Items = append(h.Items, localapi.HealthItem{Level: localapi.HealthProblem,
			Text: "Disk almost full on this PC: " + inventory.HumanBytes(st.FreeBytes) + " left"})
	}
	if !in.paired {
		h.Items = append(h.Items, localapi.HealthItem{Level: localapi.HealthWarning,
			Text: "Not paired: run 'sevjala code' here and pair this PC from the controller"})
	}
	since := in.contact
	if since.IsZero() {
		since = in.started
	}
	if in.paired && in.now.Sub(since) > silentAfter {
		text := "The controller has not contacted this PC since " + localapi.When(in.contact, in.now)
		if in.contact.IsZero() {
			text = "The controller has not contacted this PC since this agent started at " + localapi.When(in.started, in.now)
		}
		h.Items = append(h.Items, localapi.HealthItem{Level: localapi.HealthWarning, Text: text})
	}
	for _, p := range st.Problems {
		h.Items = append(h.Items, localapi.HealthItem{Level: localapi.HealthWarning, Text: p})
	}
	headline := fmt.Sprintf("Holding %s; the controller last checked in at %s", plural(st.Backups, "backup", "backups"), localapi.When(in.contact, in.now))
	if in.contact.IsZero() {
		headline = fmt.Sprintf("Holding %s; waiting for the controller to check in", plural(st.Backups, "backup", "backups"))
	}
	return finish(h, headline)
}

// finish sorts problems before warnings and sets the level; with any item
// the headline is the first one.
func finish(h localapi.Health, ok string) localapi.Health {
	slices.SortStableFunc(h.Items, func(a, b localapi.HealthItem) int { return rank(b.Level) - rank(a.Level) })
	h.Level, h.Headline = localapi.HealthOK, ok
	if len(h.Items) > 0 {
		h.Level, h.Headline = h.Items[0].Level, h.Items[0].Text
	}
	return h
}

func rank(level string) int {
	switch level {
	case localapi.HealthProblem:
		return 2
	case localapi.HealthWarning:
		return 1
	}
	return 0
}

func verifiedCopies(b store.Backup) int {
	n := 0
	for _, cp := range b.Copies {
		if cp.State == store.CopyVerified {
			n++
		}
	}
	return n
}

func plannedCopies(b store.Backup) int {
	n := 0
	for _, cp := range b.Copies {
		if cp.State != store.CopyDeleted {
			n++
		}
	}
	return n
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
