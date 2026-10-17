package retention

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)

func id(n int) string { return fmt.Sprintf("BK-20261013-%06d-0000", n) }

// bk is backup n of database shop, n minutes after t0, staged, with no copies.
func bk(n int, tags ...string) Backup {
	return Backup{ID: id(n), Database: "shop", SHA256: fmt.Sprintf("sha%d", n), Created: t0.Add(time.Duration(n) * time.Minute),
		Size: 10, Tags: tags, Staged: true, Copies: map[string]Copy{}}
}

// on records a verified copy of b on each agent, in the store and on the node.
func on(b Backup, agents ...string) Backup {
	for _, a := range agents {
		b.Copies[a] = Copy{State: "verified", VerifiedAt: t0}
	}
	return b
}

func unstaged(b Backup) Backup { b.Staged = false; return b }

// hashFailed records that the controller read b's copy on agent and its sha256 did not match.
func hashFailed(b Backup, agent string) Backup {
	b.Copies[agent] = Copy{State: "corrupt", HashFailed: true}
	return b
}

// node is a polled node holding the given backups, verified with their hash.
func node(agent string, held ...Backup) Node {
	n := Node{AgentID: agent, Polled: true, PolledAt: t0.Add(24 * time.Hour), FreeBytes: 1 << 40, Actual: map[string]Held{}}
	for _, b := range held {
		n.Actual[b.ID] = Held{State: "verified", SHA256: b.SHA256}
	}
	return n
}

var keep2 = []Policy{{Name: "p", Keep: 2}}

func TestCompute(t *testing.T) {
	b1, b2, b3 := on(bk(1, "p"), "A"), on(bk(2, "p"), "A"), on(bk(3, "p"), "A")
	tests := []struct {
		name string
		in   Input
		want Plan
	}{
		{"keeps the newest N verified on the node", Input{Policies: keep2, Backups: []Backup{b1, b2, b3},
			Nodes: []Node{node("A", b1, b2, b3)}, StagingKeep: 10},
			Plan{Deletes: []Action{{id(1), "A", "p keeps only the newest 2"}}}},

		{"new backups not arriving keep the old ones", Input{Policies: keep2, Backups: []Backup{b1, b2, bk(3, "p")},
			Nodes: []Node{node("A", b1, b2)}, StagingKeep: 10},
			Plan{Sends: []Action{{id(3), "A", "missing on this PC"}}}},

		{"missing and not staged is reported", Input{Policies: keep2, Backups: []Backup{b1, b2, unstaged(bk(3, "p"))},
			Nodes: []Node{node("A", b1, b2)}, StagingKeep: 10},
			Plan{Kept: []Action{{id(3), "A", "missing, no staging copy"}}, Expire: []string{id(3)}}},

		{"a new node is filled", Input{Policies: keep2, Backups: []Backup{b1, b2, b3},
			Nodes: []Node{node("A", b1, b2, b3), node("B")}, StagingKeep: 10},
			Plan{Deletes: []Action{{id(1), "A", "p keeps only the newest 2"}},
				Sends: []Action{{id(2), "B", "missing on this PC"}, {id(3), "B", "missing on this PC"}}}},

		{"unknown backup is kept", Input{Policies: keep2, Backups: []Backup{b2, b3},
			Nodes: []Node{node("A", b2, b3, bk(9, "p"))}, StagingKeep: 10},
			Plan{Kept: []Action{{id(9), "A", "not made by this controller"}}}},

		{"a tag without a policy keeps its backups", Input{Policies: keep2,
			Backups: []Backup{on(bk(1, "gone"), "A"), b2, b3}, Nodes: []Node{node("A", bk(1, "gone"), b2, b3)}, StagingKeep: 10},
			Plan{}},

		{"a destination change removes the copy", Input{Policies: []Policy{{Name: "p", Keep: 2, Dest: []string{"B"}}},
			Backups: []Backup{b1}, Nodes: []Node{node("A", b1)}, StagingKeep: 10},
			Plan{Deletes: []Action{{id(1), "A", "no policy sends it here any more"}}}},

		{"an offline node gets nothing", Input{Policies: keep2, Backups: []Backup{b1, b2, b3},
			Nodes: []Node{{AgentID: "A", Actual: node("A", b1, b2, b3).Actual}}, StagingKeep: 10},
			Plan{Kept: []Action{{"", "A", ReasonOffline}}}},

		{"corrupt and staged is replaced", Input{Policies: keep2, Backups: []Backup{b1, b2},
			Nodes: []Node{{AgentID: "A", Polled: true, PolledAt: t0, FreeBytes: 1 << 40,
				Actual: map[string]Held{id(1): {"verified", "sha1"}, id(2): {"corrupt", "sha2"}}}}, StagingKeep: 10},
			Plan{Updates: []Update{{id(2), "A", "corrupt", "this PC reports its copy damaged"}},
				Deletes: []Action{{id(2), "A", "damaged: replaced with a good copy"}},
				Sends:   []Action{{id(2), "A", "replace a damaged copy"}}}},

		{"a copy the controller read as damaged is replaced although the node lists it", Input{Policies: keep2,
			Backups: []Backup{b1, hashFailed(bk(2, "p"), "A")}, Nodes: []Node{node("A", b1, bk(2, "p"))}, StagingKeep: 10},
			Plan{Deletes: []Action{{id(2), "A", "damaged: replaced with a good copy"}},
				Sends: []Action{{id(2), "A", "replace a damaged copy"}}}},

		{"a copy the controller read as damaged stays damaged without staging", Input{Policies: keep2,
			Backups: []Backup{b1, unstaged(hashFailed(bk(2, "p"), "A"))}, Nodes: []Node{node("A", b1, bk(2, "p"))}, StagingKeep: 10},
			Plan{Kept: []Action{{id(2), "A", "damaged, and the controller has no good copy left to replace it"}}}},

		{"corrupt without staging is kept", Input{Policies: keep2, Backups: []Backup{b1, unstaged(b2)},
			Nodes: []Node{{AgentID: "A", Polled: true, PolledAt: t0, FreeBytes: 1 << 40,
				Actual: map[string]Held{id(1): {"verified", "sha1"}, id(2): {"corrupt", "sha2"}}}}, StagingKeep: 10},
			Plan{Updates: []Update{{id(2), "A", "corrupt", "this PC reports its copy damaged"}},
				Kept: []Action{{id(2), "A", "damaged, and the controller has no good copy left to replace it"}}}},

		{"a wrong hash is corrupt", Input{Policies: keep2, Backups: []Backup{b1},
			Nodes: []Node{{AgentID: "A", Polled: true, PolledAt: t0, FreeBytes: 1 << 40,
				Actual: map[string]Held{id(1): {"verified", "other"}}}}, StagingKeep: 10},
			Plan{Updates: []Update{{id(1), "A", "corrupt", "this PC holds a different file (checksum mismatch)"}},
				Deletes: []Action{{id(1), "A", "damaged: replaced with a good copy"}},
				Sends:   []Action{{id(1), "A", "replace a damaged copy"}}}},

		{"waiting for free space", Input{Policies: keep2, Backups: []Backup{bk(1, "p")},
			Nodes: []Node{{AgentID: "A", Polled: true, PolledAt: t0, FreeBytes: 15, MinFree: 10, Actual: map[string]Held{}}}, StagingKeep: 10},
			Plan{Kept: []Action{{id(1), "A", "waiting for free space"}}}},

		{"a copy gone from the node is failed and sent again", Input{Policies: keep2, Backups: []Backup{b1},
			Nodes: []Node{node("A")}, StagingKeep: 10},
			Plan{Updates: []Update{{id(1), "A", "failed", "missing from the node"}}, Sends: []Action{{id(1), "A", "missing on this PC"}}}},

		{"a copy verified during the poll is left alone", Input{Policies: keep2,
			Backups: []Backup{{ID: id(1), Database: "shop", SHA256: "sha1", Created: t0, Size: 10, Tags: []string{"p"}, Staged: true,
				Copies: map[string]Copy{"A": {State: "verified", VerifiedAt: t0}}}},
			Nodes: []Node{{AgentID: "A", Polled: true, PolledAt: t0, FreeBytes: 1 << 40, Actual: map[string]Held{}}}, StagingKeep: 10},
			Plan{}},

		{"a copy in flight is not sent again", Input{Policies: keep2,
			Backups: []Backup{{ID: id(1), Database: "shop", SHA256: "sha1", Created: t0, Size: 10, Tags: []string{"p"}, Staged: true,
				Copies: map[string]Copy{"A": {State: "transferring"}}}},
			Nodes: []Node{node("A")}, StagingKeep: 10},
			Plan{}},

		{"a copy found on the node is verified", Input{Policies: keep2, Backups: []Backup{bk(1, "p")},
			Nodes: []Node{node("A", bk(1, "p"))}, StagingKeep: 10},
			Plan{Updates: []Update{{id(1), "A", "verified", ""}}}},

		{"keep is counted per database", Input{Policies: []Policy{{Name: "p", Keep: 1}},
			Backups: []Backup{b1, func() Backup { b := on(bk(2, "p"), "A"); b.Database = "crm"; return b }()},
			Nodes:   []Node{node("A", b1, bk(2, "p"))}, StagingKeep: 10},
			Plan{}},

		{"staging keeps the newest, busy and still-needed backups", Input{Policies: []Policy{{Name: "p", Keep: 5}},
			Backups: []Backup{on(bk(1, "p"), "A", "B"), on(bk(2, "p"), "A", "B"), on(bk(3, "p"), "A", "B"),
				func() Backup { b := on(bk(4, "p"), "A"); b.Copies["B"] = Copy{State: "failed"}; return b }(),
				on(bk(5, "p"), "A", "B")},
			Nodes:       []Node{node("A", bk(1, "p"), bk(2, "p"), bk(3, "p"), bk(4, "p"), bk(5, "p")), {AgentID: "B"}},
			StagingKeep: 1, Busy: map[string]bool{id(2): true}},
			Plan{Unstage: []string{id(1), id(3)}, Kept: []Action{{"", "B", ReasonOffline}}}},

		{"an unstaged backup with no copy left expires", Input{Policies: []Policy{{Name: "p", Keep: 1}},
			Backups: []Backup{b1, b2}, Nodes: []Node{node("A", b1, b2)}, StagingKeep: 1},
			Plan{Deletes: []Action{{id(1), "A", "p keeps only the newest 1"}},
				Unstage: []string{id(1)}, Expire: []string{id(1)}}},

		{"a destination change keeps the only copy until the new node has it", Input{Policies: []Policy{{Name: "p", Keep: 2, Dest: []string{"B"}}},
			Backups: []Backup{unstaged(b1)}, Nodes: []Node{node("A", b1), node("B")}, StagingKeep: 10},
			Plan{Kept: []Action{{id(1), "A", ReasonStillNeeded}, {id(1), "B", "missing, no staging copy"}}}},

		{"an expired backup back on a node is kept", Input{Policies: keep2,
			Backups: []Backup{func() Backup { b := unstaged(bk(1, "p")); b.Expired = true; return b }(), b2, b3},
			Nodes:   []Node{node("A", bk(1, "p"), b2, b3)}, StagingKeep: 10},
			Plan{Updates: []Update{{id(1), "A", "verified", ""}}, Deletes: []Action{{id(1), "A", "p keeps only the newest 2"}}}},

		{"an expired backup within keep is not deleted", Input{Policies: []Policy{{Name: "p", Keep: 5}},
			Backups: []Backup{func() Backup { b := unstaged(bk(1, "p")); b.Expired = true; return b }(), b2},
			Nodes:   []Node{node("A", bk(1, "p"), b2)}, StagingKeep: 10},
			Plan{Updates: []Update{{id(1), "A", "verified", ""}}}},

		{"a backup dated in the future stops deleting, unstaging and expiring", Input{Policies: keep2,
			Backups: []Backup{b1, b2, b3, unstaged(bk(4, "p"))}, Nodes: []Node{node("A", b1, b2, b3)}, StagingKeep: 1, Now: t0.Add(150 * time.Second)},
			Plan{Kept: []Action{{"", "", ReasonClockBehind}, {id(4), "A", "missing, no staging copy"}}}},
	}
	for _, tt := range tests {
		if tt.in.Now.IsZero() {
			tt.in.Now = t0.Add(48 * time.Hour)
		}
		t.Run(tt.name, func(t *testing.T) {
			if got := Compute(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func hasAction(as []Action, a Action) bool {
	for _, x := range as {
		if x == a {
			return true
		}
	}
	return false
}

func TestLastGoodCopy(t *testing.T) {
	toB := []Policy{{Name: "p", Keep: 2, Dest: []string{"B"}}}
	lastGood := Action{id(1), "A", ReasonLastGood}
	tests := []struct {
		name         string
		in           func() Input
		kept, delete bool // b1 on A
	}{
		{"dropping a destination while the newest is verified nowhere else", func() Input {
			b0, b1 := on(bk(0, "p"), "A"), on(bk(1, "p"), "A")
			return Input{Policies: toB, Backups: []Backup{b0, b1}, Nodes: []Node{node("A", b0, b1), node("B")},
				StagingKeep: 10, Databases: []string{"shop"}}
		}, true, false},
		{"a database no longer configured gets no guard", func() Input {
			b0, b1 := on(bk(0, "p"), "A"), on(bk(1, "p"), "A")
			return Input{Policies: toB, Backups: []Backup{b0, b1}, Nodes: []Node{node("A", b0, b1), node("B")}, StagingKeep: 10}
		}, false, true},
		{"a newer verified copy on an offline PC is the last good one", func() Input {
			b1, b2 := on(bk(1, "p"), "A"), on(bk(2, "p"), "C")
			return Input{Policies: toB, Backups: []Backup{b1, b2}, Nodes: []Node{node("A", b1), node("B"), {AgentID: "C"}},
				StagingKeep: 10, Databases: []string{"shop"}}
		}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.in()
			in.Now = t0.Add(48 * time.Hour)
			p := Compute(in)
			if hasAction(p.Kept, lastGood) != tt.kept {
				t.Errorf("kept %+v", p.Kept)
			}
			if deleted := hasAction(p.Deletes, Action{id(1), "A", "no policy sends it here any more"}); deleted != tt.delete {
				t.Errorf("deletes %+v", p.Deletes)
			}
			if tt.kept && !hasAction(p.Deletes, Action{id(0), "A", "no policy sends it here any more"}) {
				t.Errorf("the older backup should still go: %+v", p.Deletes)
			}
		})
	}
}

func TestLastGoodLeavesPolicyReasonsAlone(t *testing.T) {
	b1, b2 := on(bk(1, "p"), "A"), on(bk(2, "p"), "A")
	p := Compute(Input{Policies: []Policy{{Name: "p", Keep: 1}}, Backups: []Backup{b1, b2}, Nodes: []Node{node("A", b1, b2)},
		StagingKeep: 10, Databases: []string{"shop"}, Now: t0.Add(48 * time.Hour)})
	want := Plan{Deletes: []Action{{id(1), "A", "p keeps only the newest 1"}}}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("got %+v", p)
	}
}
