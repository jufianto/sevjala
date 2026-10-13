package coord

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/jufianto/sevjala/dump/dumptest"
	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/store"
)

// backupAt runs a backup of shop created at the given time and waits for want.
func backupAt(t *testing.T, ctrl *Controller, at time.Time, want string, tags ...string) string {
	t.Helper()
	if len(tags) == 0 {
		tags = []string{"manual"}
	}
	jobs, err := ctrl.start([]string{"shop"}, tags, at, ctrl.config())
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, ctrl, jobs[0].BackupID, state(want))
	return jobs[0].BackupID
}

func stored(t *testing.T, n testAgent) []string {
	t.Helper()
	inv, err := n.inv.Scan()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range inv.Backups {
		if c.State == inventory.StateVerified {
			ids = append(ids, c.BackupID)
		}
	}
	return ids
}

func copyState(t *testing.T, ctrl *Controller, id, agent string) string {
	t.Helper()
	b, err := ctrl.Backup(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range b.Copies {
		if c.AgentID == agent {
			return c.State
		}
	}
	return ""
}

func TestReconcileDeletesBeyondKeep(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	n := startAgent(t, "node", "PC-1")
	pairNode(t, ctrl, n)
	// A disabled policy still applies its keep.
	if _, err := ctrl.SetPolicy(ctx, "manual", localapi.PolicyChange{Keep: ptr(1), Enabled: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	old := backupAt(t, ctrl, tick0, store.BackupDistributed)
	newer := backupAt(t, ctrl, tick0.Add(time.Minute), store.BackupDistributed)
	ctrl.PollAll(ctx)

	plan, err := ctrl.Reconcile(ctx, true)
	want := []localapi.RetentionAction{{BackupID: old, AgentID: n.id.AgentID, Hostname: "PC-1",
		Reason: "retention: not among the newest 1 manual on this node"}}
	if err != nil || !plan.DryRun || !slices.Equal(plan.Deletes, want) || len(plan.Sends) != 0 {
		t.Fatalf("dry run %+v, %v", plan, err)
	}
	if got := stored(t, n); len(got) != 2 {
		t.Fatalf("a dry run deleted: %v", got)
	}
	if plan, err = ctrl.Reconcile(ctx, false); err != nil || plan.DryRun || !slices.Equal(plan.Deletes, want) {
		t.Fatalf("run %+v, %v", plan, err)
	}
	if got := stored(t, n); !slices.Equal(got, []string{newer}) {
		t.Errorf("node holds %v", got)
	}
	if st := copyState(t, ctrl, old, n.id.AgentID); st != store.CopyDeleted {
		t.Errorf("copy %s", st)
	}
	// Before the next poll, the last inventory still lists the deleted copy.
	if plan, err := ctrl.Reconcile(ctx, false); err != nil || len(plan.Deletes)+len(plan.Sends) != 0 {
		t.Errorf("second run before a poll %+v, %v", plan, err)
	}
	if st := copyState(t, ctrl, old, n.id.AgentID); st != store.CopyDeleted {
		t.Errorf("copy after a second run %s", st)
	}
	ctrl.PollAll(ctx)
	if plan, _ := ctrl.Reconcile(ctx, true); len(plan.Deletes)+len(plan.Sends)+len(plan.Unstaged)+len(plan.Expired) != 0 {
		t.Errorf("not settled: %+v", plan)
	}
}

func TestReconcileFillsANewNode(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	pairNode(t, ctrl, startAgent(t, "node", "PC-1"))
	id := backupAt(t, ctrl, tick0, store.BackupDistributed)
	late := startAgent(t, "node", "PC-2")
	pairNode(t, ctrl, late)
	ctrl.PollAll(ctx)
	plan, err := ctrl.Reconcile(ctx, false)
	if err != nil || len(plan.Sends) != 1 || plan.Sends[0].Hostname != "PC-2" || plan.Sends[0].Reason != "backfill" {
		t.Fatalf("plan %+v, %v", plan, err)
	}
	b := waitState(t, ctrl, id, state(store.BackupDistributed))
	if len(b.Copies) != 2 || !slices.Equal(stored(t, late), []string{id}) {
		t.Errorf("backup %+v", b)
	}
}

func TestReconcileReplacesACorruptCopy(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	n := startAgent(t, "node", "PC-1")
	pairNode(t, ctrl, n)
	id := backupAt(t, ctrl, tick0, store.BackupDistributed)
	if err := os.Truncate(filepath.Join(n.inv.Dir(), id+inventory.DataExt), 3); err != nil {
		t.Fatal(err)
	}
	ctrl.PollAll(ctx)
	plan, err := ctrl.Reconcile(ctx, false)
	if err != nil || len(plan.Deletes) != 1 || plan.Deletes[0].Reason != "corrupt: replaced from staging" || len(plan.Sends) != 1 {
		t.Fatalf("plan %+v, %v", plan, err)
	}
	waitState(t, ctrl, id, state(store.BackupDistributed))
	if got := stored(t, n); !slices.Equal(got, []string{id}) {
		t.Errorf("node holds %v", got)
	}
}

func TestReconcilePrunesStaging(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	ctrl.Config.Controller.Staging.Keep = 1
	pairNode(t, ctrl, startAgent(t, "node", "PC-1"))
	old := backupAt(t, ctrl, tick0, store.BackupDistributed)
	newer := backupAt(t, ctrl, tick0.Add(time.Minute), store.BackupDistributed)
	orphan := "BK-20200101-000000-0001"
	for _, ext := range []string{inventory.DataExt, inventory.ManifestExt} {
		if err := os.WriteFile(filepath.Join(ctrl.Staging, orphan+ext), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctrl.PollAll(ctx)
	plan, err := ctrl.Reconcile(ctx, false)
	if err != nil || !slices.Equal(plan.Unstaged, []string{old}) {
		t.Fatalf("plan %+v, %v", plan, err)
	}
	if got := names(t, ctrl.Staging); !slices.Equal(got, []string{newer + inventory.ManifestExt, newer + inventory.DataExt}) {
		t.Errorf("staging %v", got)
	}
	if b, _ := ctrl.Store.Backup(ctx, old); b.StagingPath != "" || b.State != store.BackupDistributed {
		t.Errorf("unstaged backup %+v", b)
	}
}

func TestReconcileExpiresWhatIsGoneEverywhere(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	ctrl.Config.Controller.Staging.Keep = 1
	pairNode(t, ctrl, startAgent(t, "node", "PC-1"))
	if _, err := ctrl.SetPolicy(ctx, "manual", localapi.PolicyChange{Keep: ptr(1)}); err != nil {
		t.Fatal(err)
	}
	old := backupAt(t, ctrl, tick0, store.BackupDistributed)
	backupAt(t, ctrl, tick0.Add(time.Minute), store.BackupDistributed)
	ctrl.PollAll(ctx)
	if plan, err := ctrl.Reconcile(ctx, false); err != nil || !slices.Equal(plan.Expired, []string{old}) {
		t.Fatalf("plan %+v, %v", plan, err)
	}
	live, _ := ctrl.Backups(ctx, false)
	all, _ := ctrl.Backups(ctx, true)
	if len(live) != 1 || len(all) != 2 {
		t.Errorf("live %d, all %d", len(live), len(all))
	}
}

func TestReconcileKeepsUnknownBackups(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	n := startAgent(t, "node", "PC-1")
	pairNode(t, ctrl, n)
	data := []byte("from an older controller")
	m := inventory.Manifest{V: 1, BackupID: "BK-20250101-010000-0001", Database: "shop", CreatedAt: tick0,
		Size: int64(len(data)), SHA256: sha256Hex(data), Tags: []string{"daily"}}
	if err := n.inv.Receive(m, m.SHA256, m.Size, bytesReader(data)); err != nil {
		t.Fatal(err)
	}
	ctrl.PollAll(ctx)
	plan, err := ctrl.Reconcile(ctx, false)
	if err != nil || len(plan.Kept) != 1 || plan.Kept[0].BackupID != m.BackupID || plan.Kept[0].Reason != "not made by this controller" {
		t.Fatalf("plan %+v, %v", plan, err)
	}
	if got := stored(t, n); !slices.Equal(got, []string{m.BackupID}) {
		t.Errorf("node holds %v", got)
	}
}

// An unreadable store is no inventory at all, not an empty one.
func TestReconcileSkipsAnUnreadableStore(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a non-root Unix user to make the store unreadable")
	}
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	n := startAgent(t, "node", "PC-1")
	pairNode(t, ctrl, n)
	id := backupAt(t, ctrl, tick0, store.BackupDistributed)
	if err := os.Chmod(n.inv.Dir(), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(n.inv.Dir(), 0o700) })
	ctrl.PollAll(ctx)
	plan, err := ctrl.Reconcile(ctx, false)
	if err != nil || len(plan.Sends) != 0 || len(plan.Kept) != 1 || plan.Kept[0].BackupID != "" {
		t.Fatalf("plan %+v, %v", plan, err)
	}
	if st := copyState(t, ctrl, id, n.id.AgentID); st != store.CopyVerified {
		t.Errorf("copy %s", st)
	}
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
