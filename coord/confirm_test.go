package coord

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/dump/dumptest"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/retention"
	"github.com/jufianto/sevjala/store"
)

// threeBackups pairs PC-1 and sends it three backups tagged tag; poll asks it
// what it holds afterwards.
func threeBackups(t *testing.T, tag string, poll bool) (*Controller, testAgent) {
	t.Helper()
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	pc1 := startAgent(t, "node", "PC-1")
	pairNode(t, ctrl, pc1)
	for i := range 3 {
		backupAt(t, ctrl, tick0.Add(time.Duration(i)*time.Minute), store.BackupDistributed, tag)
	}
	if poll {
		ctrl.PollAll(context.Background())
	}
	return ctrl, pc1
}

func wantConfirm(t *testing.T, err error, word string, deletes int) {
	t.Helper()
	e, ok := localapi.AsConfirm(err)
	if !ok {
		t.Fatalf("want confirm_required, got %v", err)
	}
	if e.Code != 409 || e.Word != word || e.Deletes != deletes || !slices.Equal(e.Hosts, []string{"PC-1"}) {
		t.Errorf("confirm %+v", e)
	}
}

func TestPolicyChangeNeedsConfirmWhenItDeletes(t *testing.T) {
	ctrl, _ := threeBackups(t, "daily", true)
	ctx := context.Background()

	if _, err := ctrl.SetPolicy(ctx, "daily", localapi.PolicyChange{Keep: ptr(40)}); err != nil {
		t.Fatalf("raising keep: %v", err)
	}
	_, err := ctrl.SetPolicy(ctx, "daily", localapi.PolicyChange{Keep: ptr(1)})
	wantConfirm(t, err, "daily", 2)

	p, err := ctrl.SetPolicy(ctx, "daily", localapi.PolicyChange{Keep: ptr(1), DryRun: true})
	if err != nil || len(p.Deletes) != 2 || p.Keep != 1 {
		t.Fatalf("dry run %+v, %v", p, err)
	}
	if sp, _ := ctrl.Store.Policy(ctx, "daily"); sp.Keep != 40 {
		t.Errorf("the dry run saved keep %d", sp.Keep)
	}
	if p, err := ctrl.SetPolicy(ctx, "daily", localapi.PolicyChange{Keep: ptr(1), Confirm: "daily"}); err != nil || p.Keep != 1 {
		t.Errorf("confirmed %+v, %v", p, err)
	}
}

func TestAddingAPolicyOverAUsedTagNeedsConfirm(t *testing.T) {
	ctrl, _ := threeBackups(t, "weekly", true)
	ctx := context.Background()
	_, err := ctrl.AddPolicy(ctx, localapi.PolicyChange{Name: "weekly", Keep: ptr(1)})
	wantConfirm(t, err, "weekly", 2)
	if _, err := ctrl.AddPolicy(ctx, localapi.PolicyChange{Name: "yearly", Keep: ptr(1)}); err != nil {
		t.Errorf("a new tag: %v", err)
	}
	if _, err := ctrl.AddPolicy(ctx, localapi.PolicyChange{Name: "weekly", Keep: ptr(1), Confirm: "weekly"}); err != nil {
		t.Errorf("confirmed: %v", err)
	}
}

func TestPreviewCountsOfflinePCs(t *testing.T) {
	ctrl, _ := threeBackups(t, "daily", false)
	_, err := ctrl.SetPolicy(context.Background(), "daily", localapi.PolicyChange{Keep: ptr(1)})
	wantConfirm(t, err, "daily", 2)
}

func TestPreviewSeesPastAClockBehind(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	pairNode(t, ctrl, startAgent(t, "node", "PC-1"))
	ahead := time.Now().Add(time.Hour)
	for i := range 3 {
		backupAt(t, ctrl, ahead.Add(time.Duration(i)*time.Minute), store.BackupDistributed, "daily")
	}
	ctrl.PollAll(context.Background())
	_, err := ctrl.SetPolicy(context.Background(), "daily", localapi.PolicyChange{Keep: ptr(1)})
	wantConfirm(t, err, "daily", 2)
}

func TestConfirmNamesPCsSharingAHostname(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	a, b := startAgent(t, "node", "PC-1"), startAgent(t, "node", "PC-1")
	pairNode(t, ctrl, a)
	pairNode(t, ctrl, b)
	for i := range 2 {
		backupAt(t, ctrl, tick0.Add(time.Duration(i)*time.Minute), store.BackupDistributed, "daily")
	}
	ctrl.PollAll(context.Background())
	_, err := ctrl.SetPolicy(context.Background(), "daily", localapi.PolicyChange{Keep: ptr(1)})
	e, ok := localapi.AsConfirm(err)
	if !ok || e.Deletes != 2 {
		t.Fatalf("err %v", err)
	}
	want := []string{"PC-1 (" + a.id.AgentID[:8] + ")", "PC-1 (" + b.id.AgentID[:8] + ")"}
	slices.Sort(want)
	if !slices.Equal(e.Hosts, want) || !strings.Contains(e.Msg, strings.Join(want, ", ")) {
		t.Errorf("hosts %q, msg %q", e.Hosts, e.Msg)
	}
}

func TestConfirmDoesNotBypassLastGood(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	pc1, pc2 := startAgent(t, "node", "PC-1"), startAgent(t, "node", "PC-2")
	pairNode(t, ctrl, pc1)
	if _, err := ctrl.SetPolicy(ctx, "daily", localapi.PolicyChange{To: []string{"PC-1"}}); err != nil {
		t.Fatal(err)
	}
	newest := ""
	for i := range 2 {
		newest = backupAt(t, ctrl, tick0.Add(time.Duration(i)*time.Minute), store.BackupDistributed, "daily")
	}
	pairNode(t, ctrl, pc2)
	ctrl.PollAll(ctx)
	// PC-2 can get the newest from the controller's own copy, but until it has, PC-1
	// holds its only verified copy.
	if _, err := ctrl.SetPolicy(ctx, "daily", localapi.PolicyChange{To: []string{"PC-2"}, Confirm: "daily"}); err != nil {
		t.Fatal(err)
	}
	plan, err := ctrl.Reconcile(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(plan.Kept, func(a localapi.RetentionAction) bool {
		return a.BackupID == newest && a.AgentID == pc1.id.AgentID && a.Reason == retention.ReasonLastGood
	}) {
		t.Errorf("kept %+v, deletes %+v", plan.Kept, plan.Deletes)
	}
}

func TestReplaceAnswersConfirmRequired(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	backupAt(t, ctrl, tick0, store.BackupCreated)
	_, err := ctrl.Restore(context.Background(), localapi.RestoreRequest{Backup: "latest", Replace: true})
	e, ok := localapi.AsConfirm(err)
	if !ok || e.Word != "shop" || !strings.Contains(e.Msg, "backs shop up first") || !strings.Contains(e.Msg, "unavailable until loading ends") {
		t.Errorf("err %v", err)
	}
}
