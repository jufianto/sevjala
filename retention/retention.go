// Package retention computes what reconciliation does: which copies to delete,
// which to send again, what staging can drop (blueprint: Backup engine →
// Retention and reconciliation). It does no I/O; the same input gives the
// same plan.
package retention

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/store"
)

type Policy struct {
	Name string
	Keep int
	Dest []string // empty = every node
}

type Copy struct {
	State      string
	VerifiedAt time.Time
	HashFailed bool // the controller read this copy and its sha256 did not match
}

type Backup struct {
	ID, Database, SHA256 string
	Created              time.Time
	Size                 int64
	Tags                 []string
	Staged               bool // the staging file is present
	Expired              bool
	Copies               map[string]Copy // agent_id → the store's copy
}

// Held is one entry of a node's /status inventory.
type Held struct{ State, SHA256 string }

type Node struct {
	AgentID   string
	Polled    bool // online or warning: Actual is the inventory of the last poll
	PolledAt  time.Time
	FreeBytes int64
	MinFree   int64
	Actual    map[string]Held
}

type Input struct {
	Policies    []Policy
	Backups     []Backup
	Nodes       []Node // every paired node
	StagingKeep int
	Busy        map[string]bool // still dumping: never unstaged
	Now         time.Time
}

type Action struct{ BackupID, AgentID, Reason string }

type Update struct{ BackupID, AgentID, State, Reason string }

type Plan struct {
	Updates []Update // copy states corrected from the nodes' inventories
	Deletes []Action
	Sends   []Action
	Kept    []Action // left alone, and why
	Unstage []string
	Expire  []string
}

const (
	ReasonUnknown     = "not made by this controller"
	ReasonOffline     = "this PC did not report what it holds (offline, or its backup folder cannot be read)"
	ReasonBackfill    = "missing on this PC"
	ReasonStillNeeded = "a PC that should hold it does not have it yet"
	ReasonClockBehind = "a backup is dated after now (clock moved back): nothing is deleted, unstaged or expired"
)

type group struct{ db, tag string }

type computer struct {
	in       Input
	plan     Plan
	byID     map[string]*Backup
	policies map[string]Policy
	groups   map[group][]*Backup          // backups per database and tag, newest first; expired ones still count as held
	keys     []group                      // groups in a fixed order, so reasons are stable
	final    map[string]map[string]string // backup → agent → copy state once the plan ran
	wants    map[string]map[string]bool   // agent → wanted, memoized
	frozen   bool                         // the clock is behind a backup: delete nothing
}

func Compute(in Input) Plan {
	c := &computer{in: in, byID: map[string]*Backup{}, policies: map[string]Policy{}, groups: map[group][]*Backup{},
		final: map[string]map[string]string{}, wants: map[string]map[string]bool{}}
	for i := range in.Backups {
		b := &in.Backups[i]
		c.byID[b.ID] = b
		c.final[b.ID] = map[string]string{}
		for a, cp := range b.Copies {
			c.final[b.ID][a] = cp.State
		}
		c.frozen = c.frozen || b.Created.After(in.Now)
		for _, t := range b.Tags {
			g := group{b.Database, t}
			c.groups[g] = append(c.groups[g], b)
		}
	}
	for _, bs := range c.groups {
		slices.SortFunc(bs, newestFirst)
	}
	c.keys = slices.SortedFunc(maps.Keys(c.groups), func(a, b group) int {
		return cmp.Or(strings.Compare(a.db, b.db), strings.Compare(a.tag, b.tag))
	})
	for _, p := range in.Policies {
		c.policies[p.Name] = p
	}
	for _, n := range in.Nodes {
		if n.Polled {
			c.node(n)
		} else {
			c.plan.Kept = append(c.plan.Kept, Action{AgentID: n.AgentID, Reason: ReasonOffline})
		}
	}
	if c.frozen {
		c.plan.Kept = append(c.plan.Kept, Action{Reason: ReasonClockBehind})
	} else {
		c.expire(c.staging())
	}
	c.plan.sort()
	return c.plan
}

func newestFirst(a, b *Backup) int {
	return cmp.Or(b.Created.Compare(a.Created), strings.Compare(b.ID, a.ID))
}

func targets(p Policy, agent string) bool { return len(p.Dest) == 0 || slices.Contains(p.Dest, agent) }

// wanted is, per database and tag whose policy sends to agent, the newest Keep
// backups that are not expired. Callers must not modify the result.
func (c *computer) wanted(agent string) map[string]bool {
	if out, ok := c.wants[agent]; ok {
		return out
	}
	out := map[string]bool{}
	for _, g := range c.keys {
		p, ok := c.policies[g.tag]
		if !ok || !targets(p, agent) {
			continue
		}
		n := 0
		for _, b := range c.groups[g] {
			if n == p.Keep {
				break
			}
			if !b.Expired {
				out[b.ID] = true
				n++
			}
		}
	}
	c.wants[agent] = out
	return out
}

// lastSource: the copy on agent may be the only way a node that wants the
// backup can still get it.
func (c *computer) lastSource(b *Backup, agent string) bool {
	if b.Staged {
		return false
	}
	for _, n := range c.in.Nodes {
		if n.AgentID != agent && c.wanted(n.AgentID)[b.ID] && c.final[b.ID][n.AgentID] != store.CopyVerified {
			return true
		}
	}
	return false
}

func (c *computer) node(n Node) {
	// The node's inventory checks size and the manifest's sha256, not the bytes; a
	// copy the controller read and found damaged stays damaged until replaced.
	verified := func(b *Backup) bool {
		h, ok := n.Actual[b.ID]
		return ok && h.State == inventory.StateVerified && h.SHA256 == b.SHA256 && !b.Copies[n.AgentID].HashFailed
	}
	// A node drops an old copy only once newer ones are verified on that same node.
	needed, why := maps.Clone(c.wanted(n.AgentID)), map[string]string{}
	for _, g := range c.keys {
		p, ok := c.policies[g.tag]
		if !ok {
			for _, b := range c.groups[g] {
				needed[b.ID] = true
			}
			continue
		}
		if !targets(p, n.AgentID) {
			continue
		}
		held := 0
		for _, b := range c.groups[g] {
			if verified(b) {
				if held < p.Keep {
					needed[b.ID] = true
				}
				held++
			}
			if why[b.ID] == "" {
				why[b.ID] = fmt.Sprintf("%s keeps only the newest %d", p.Name, p.Keep)
			}
		}
	}
	for _, b := range c.byID {
		cp, has := b.Copies[n.AgentID]
		h, onNode := n.Actual[b.ID]
		if b.Expired && (!onNode || !verified(b)) {
			continue
		}
		switch {
		case onNode && verified(b):
			if cp.State != store.CopyVerified {
				c.update(b.ID, n.AgentID, store.CopyVerified, "")
			}
		case onNode && h.State != inventory.StateVerified:
			if cp.State != store.CopyCorrupt {
				c.update(b.ID, n.AgentID, store.CopyCorrupt, "this PC reports its copy damaged")
			}
		case onNode:
			if cp.State != store.CopyCorrupt {
				c.update(b.ID, n.AgentID, store.CopyCorrupt, "this PC holds a different file (checksum mismatch)")
			}
		case has && cp.State == store.CopyVerified && !verifiedDuring(cp, n):
			c.update(b.ID, n.AgentID, store.CopyFailed, "missing from the node")
		}
	}
	for _, id := range slices.Sorted(maps.Keys(n.Actual)) {
		b := c.byID[id]
		switch {
		case b == nil:
			c.plan.Kept = append(c.plan.Kept, Action{id, n.AgentID, ReasonUnknown})
		case !needed[id] && c.lastSource(b, n.AgentID):
			c.plan.Kept = append(c.plan.Kept, Action{id, n.AgentID, ReasonStillNeeded})
		case !needed[id]:
			reason := why[id]
			if reason == "" {
				reason = "no policy sends it here any more"
			}
			c.delete(id, n.AgentID, reason)
		case verified(b):
		case b.Staged:
			if c.delete(id, n.AgentID, "damaged: replaced with a good copy") {
				c.send(id, n.AgentID, "replace a damaged copy")
			}
		default:
			c.plan.Kept = append(c.plan.Kept, Action{id, n.AgentID, "damaged, and the controller has no good copy left to replace it"})
		}
	}
	for _, id := range slices.Sorted(maps.Keys(c.wanted(n.AgentID))) {
		b := c.byID[id]
		if _, onNode := n.Actual[id]; onNode {
			continue
		}
		switch st := c.final[id][n.AgentID]; {
		case st == store.CopyPending || st == store.CopyTransferring:
		case st == store.CopyVerified && verifiedDuring(b.Copies[n.AgentID], n):
		case !b.Staged:
			c.plan.Kept = append(c.plan.Kept, Action{id, n.AgentID, "missing, no staging copy"})
		case n.FreeBytes-b.Size < n.MinFree:
			c.plan.Kept = append(c.plan.Kept, Action{id, n.AgentID, "waiting for free space"})
		default:
			c.send(id, n.AgentID, ReasonBackfill)
		}
	}
}

// verifiedDuring: the copy was verified after the poll started, so the
// inventory cannot show it yet. VerifiedAt is truncated to the second.
func verifiedDuring(cp Copy, n Node) bool { return cp.VerifiedAt.Add(time.Second).After(n.PolledAt) }

func (c *computer) update(id, agent, state, reason string) {
	c.plan.Updates = append(c.plan.Updates, Update{id, agent, state, reason})
	c.final[id][agent] = state
}

func (c *computer) delete(id, agent, reason string) bool {
	if c.frozen {
		return false
	}
	c.plan.Deletes = append(c.plan.Deletes, Action{id, agent, reason})
	c.final[id][agent] = store.CopyDeleted
	return true
}

func (c *computer) send(id, agent, reason string) {
	c.plan.Sends = append(c.plan.Sends, Action{id, agent, reason})
	c.final[id][agent] = store.CopyPending
}

// staging keeps the newest StagingKeep per database, what is still dumping,
// and what some node still lacks; the rest is unstaged.
func (c *computer) staging() map[string]bool {
	stay := maps.Clone(c.in.Busy)
	if stay == nil {
		stay = map[string]bool{}
	}
	byDB := map[string][]*Backup{}
	for _, b := range c.byID {
		if b.Staged {
			byDB[b.Database] = append(byDB[b.Database], b)
		}
	}
	for _, bs := range byDB {
		slices.SortFunc(bs, newestFirst)
		for _, b := range bs[:min(c.in.StagingKeep, len(bs))] {
			stay[b.ID] = true
		}
	}
	for _, n := range c.in.Nodes {
		for id := range c.wanted(n.AgentID) {
			if c.final[id][n.AgentID] != store.CopyVerified {
				stay[id] = true
			}
		}
	}
	unstaged := map[string]bool{}
	for id, b := range c.byID {
		if b.Staged && !stay[id] {
			c.plan.Unstage = append(c.plan.Unstage, id)
			unstaged[id] = true
		}
	}
	return unstaged
}

// expire marks backups that exist nowhere any more once the plan ran.
func (c *computer) expire(unstaged map[string]bool) {
	for id, b := range c.byID {
		if b.Expired || (b.Staged && !unstaged[id]) {
			continue
		}
		live := false
		for _, st := range c.final[id] {
			switch st {
			case store.CopyVerified, store.CopyCorrupt, store.CopyPending, store.CopyTransferring:
				live = true
			}
		}
		if !live {
			c.plan.Expire = append(c.plan.Expire, id)
		}
	}
}

func (p *Plan) sort() {
	byAction := func(a, b Action) int {
		return cmp.Or(strings.Compare(a.BackupID, b.BackupID), strings.Compare(a.AgentID, b.AgentID))
	}
	slices.SortFunc(p.Updates, func(a, b Update) int {
		return cmp.Or(strings.Compare(a.BackupID, b.BackupID), strings.Compare(a.AgentID, b.AgentID))
	})
	slices.SortFunc(p.Deletes, byAction)
	slices.SortFunc(p.Sends, byAction)
	slices.SortFunc(p.Kept, byAction)
	slices.Sort(p.Unstage)
	slices.Sort(p.Expire)
}
