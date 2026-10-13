package coord

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/dump/dumptest"
	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/store"
)

// waitIdle waits until no dump runs and no copy is pending or transferring.
func waitIdle(t *testing.T, ctrl *Controller) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		bs, err := ctrl.Backups(context.Background(), true)
		busy := err != nil
		for _, b := range bs {
			busy = busy || b.State == localapi.BackupDumping
			for _, c := range b.Copies {
				busy = busy || c.State == store.CopyPending || c.State == store.CopyTransferring
			}
		}
		if !busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("not idle: %+v, %v", bs, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// expected is what every node must hold: the newest keep backups of each tag.
func expected(t *testing.T, ctrl *Controller, keep map[string]int) []string {
	t.Helper()
	bs, err := ctrl.Backups(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for tag, n := range keep {
		for _, b := range bs { // newest first
			if n > 0 && slices.Contains(b.Tags, tag) {
				n--
				if !slices.Contains(out, b.BackupID) {
					out = append(out, b.BackupID)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// TestUnattended is the exit criterion on loopback with a fake clock: the
// schedule runs, a node goes offline and comes back, and every node ends up
// with exactly the backups its policies keep.
func TestUnattended(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	ctrl.Config.Controller.Staging.Keep = 2
	var nodes []*testAgent
	for _, h := range []string{"PC-1", "PC-2", "PC-3"} {
		n := startAgent(t, "node", h)
		pairNode(t, ctrl, n)
		nodes = append(nodes, &n)
	}
	for name, ch := range map[string]localapi.PolicyChange{
		"frequent": {Keep: ptr(2)}, "hourly": {Keep: ptr(2)}, "daily": {Enabled: ptr(false)}, "monthly": {Enabled: ptr(false)},
	} {
		if _, err := ctrl.SetPolicy(ctx, name, ch); err != nil {
			t.Fatal(err)
		}
	}
	keep := map[string]int{"frequent": 2, "hourly": 2}
	settle := func() {
		waitIdle(t, ctrl)
		ctrl.PollAll(ctx)
		if _, err := ctrl.Reconcile(ctx, false); err != nil {
			t.Fatal(err)
		}
		waitIdle(t, ctrl)
	}
	check := func(when string, ns ...*testAgent) {
		t.Helper()
		want := expected(t, ctrl, keep)
		for _, n := range ns {
			if got := stored(t, *n); !slices.Equal(got, want) {
				t.Fatalf("%s: %s holds %v, want %v", when, n.host, got, want)
			}
		}
	}

	now := time.Date(2026, 1, 13, 10, 0, 0, 0, time.UTC) // before the real clock, or retention freezes
	for ; now.Hour() < 11 || now.Minute() == 0; now = now.Add(10 * time.Minute) {
		ctrl.Tick(ctx, now)
		settle()
		check(now.Format("15:04"), nodes...)
		if now.Hour() == 11 {
			break
		}
	}
	if got := len(expected(t, ctrl, keep)); got != 3 {
		t.Fatalf("expected set has %d backups, want 3 (2 frequent, 2 hourly sharing 11:00)", got)
	}

	// PC-3 goes away for two scheduled runs.
	nodes[2].stop(t)
	for range offlineAfter {
		ctrl.PollAll(ctx)
	}
	if a := agentByHost(t, ctrl, "PC-3"); a.State != store.AgentOffline {
		t.Fatalf("PC-3 %+v", a)
	}
	before := stored(t, *nodes[2])
	for range 2 {
		now = now.Add(10 * time.Minute)
		ctrl.Tick(ctx, now)
		settle()
	}
	check("PC-3 away", nodes[0], nodes[1])
	if got := stored(t, *nodes[2]); !slices.Equal(got, before) {
		t.Fatalf("an offline node was touched: %v, was %v", got, before)
	}

	// It comes back and is filled in, then trimmed.
	nodes[2].restart(t, ctrl)
	for range 3 {
		settle()
	}
	check("PC-3 back", nodes...)

	// A copy damaged on disk is replaced from staging.
	victim := expected(t, ctrl, map[string]int{"frequent": 1})[0] // still staged
	if err := os.Truncate(filepath.Join(nodes[0].inv.Dir(), victim+inventory.DataExt), 5); err != nil {
		t.Fatal(err)
	}
	settle()
	settle()
	check("after corruption", nodes...)

	// Staging keeps the newest two.
	var staged []string
	for _, name := range names(t, ctrl.Staging) {
		if id, ok := strings.CutSuffix(name, inventory.DataExt); ok {
			staged = append(staged, id)
		}
	}
	if all := expected(t, ctrl, map[string]int{"frequent": 2}); !slices.Equal(staged, all) {
		t.Errorf("staging %v, want %v", staged, all)
	}

	ctrl.PollAll(ctx)
	plan, err := ctrl.Reconcile(ctx, true)
	if err != nil || len(plan.Deletes)+len(plan.Sends)+len(plan.Unstaged)+len(plan.Expired)+len(plan.Kept) != 0 {
		t.Errorf("not settled: %+v, %v", plan, err)
	}
	for _, id := range expected(t, ctrl, keep) {
		b, _ := ctrl.Backup(ctx, id)
		for _, c := range b.Copies {
			if c.State != store.CopyVerified {
				t.Errorf("%s on %s: %s", id, c.Hostname, c.State)
			}
		}
	}
}
