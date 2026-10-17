package coord

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jufianto/sevjala/dump/dumptest"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/store"
)

func ptr[T any](v T) *T { return &v }

func TestPolicies(t *testing.T) {
	ctx := context.Background()
	ctrl := newController(t)
	ps, err := ctrl.Policies(ctx)
	if err != nil || len(ps) != 6 || ps[0].Name != "frequent" || ps[4].Name != "manual" || ps[5].Name != "pre-restore" {
		t.Fatalf("defaults %+v, %v", ps, err)
	}
	if ps[0].NextRun.IsZero() || !ps[4].NextRun.IsZero() || len(ps[0].To) != 0 {
		t.Errorf("frequent %+v, manual %+v", ps[0], ps[4])
	}

	p, err := ctrl.AddPolicy(ctx, localapi.PolicyChange{Name: "weekly", Schedule: ptr("@weekly"), Keep: ptr(4)})
	if err != nil || p.Keep != 4 || !p.Enabled || p.NextRun.IsZero() {
		t.Fatalf("add %+v, %v", p, err)
	}
	p, err = ctrl.SetPolicy(ctx, "weekly", localapi.PolicyChange{Keep: ptr(8), Enabled: ptr(false)})
	if err != nil || p.Keep != 8 || p.Enabled || !p.NextRun.IsZero() || p.Schedule != "@weekly" {
		t.Errorf("set %+v, %v", p, err)
	}
	if err := ctrl.RemovePolicy(ctx, "weekly"); err != nil {
		t.Fatal(err)
	}

	errs := []struct {
		name string
		err  error
		code int
		msg  string
	}{
		{"duplicate", second(ctrl.AddPolicy(ctx, localapi.PolicyChange{Name: "daily", Keep: ptr(1)})), http.StatusConflict, "already exists"},
		{"bad name", second(ctrl.AddPolicy(ctx, localapi.PolicyChange{Name: "Every Day", Keep: ptr(1)})), http.StatusBadRequest, "policy name"},
		{"no keep", second(ctrl.AddPolicy(ctx, localapi.PolicyChange{Name: "x"})), http.StatusBadRequest, "keep"},
		{"keep 0", second(ctrl.AddPolicy(ctx, localapi.PolicyChange{Name: "x", Keep: ptr(0)})), http.StatusBadRequest, "1-1000"},
		{"bad cron", second(ctrl.AddPolicy(ctx, localapi.PolicyChange{Name: "x", Keep: ptr(1), Schedule: ptr("61 * * * *")})), http.StatusBadRequest, "minute"},
		{"unknown node", second(ctrl.AddPolicy(ctx, localapi.PolicyChange{Name: "x", Keep: ptr(1), To: []string{"PC-9"}})), http.StatusNotFound, "PC-9"},
		{"set unknown", second(ctrl.SetPolicy(ctx, "nope", localapi.PolicyChange{Keep: ptr(1)})), http.StatusNotFound, "no policy nope"},
		{"remove manual", ctrl.RemovePolicy(ctx, "manual"), http.StatusConflict, "backup now"},
		{"remove pre-restore", ctrl.RemovePolicy(ctx, "pre-restore"), http.StatusConflict, "--replace-production"},
		{"remove unknown", ctrl.RemovePolicy(ctx, "nope"), http.StatusNotFound, "no policy nope"},
	}
	for _, e := range errs {
		if errCode(e.err) != e.code || !strings.Contains(e.err.Error(), e.msg) {
			t.Errorf("%s: %v", e.name, e.err)
		}
	}
}

func second[T any](_ T, err error) error { return err }

func TestPolicyDestinations(t *testing.T) {
	ctx := context.Background()
	ctrl := newController(t)
	pairNode(t, ctrl, startAgent(t, "node", "PC-1"))
	pairNode(t, ctrl, startAgent(t, "node", "PC-2"))
	p, err := ctrl.SetPolicy(ctx, "daily", localapi.PolicyChange{To: []string{"pc-2", "PC-1", "pc-2"}})
	if err != nil || len(p.To) != 2 {
		t.Fatalf("set %+v, %v", p, err)
	}
	hosts := []string{p.To[0].Hostname, p.To[1].Hostname}
	slices.Sort(hosts)
	if hosts[0] != "PC-1" || hosts[1] != "PC-2" {
		t.Errorf("to %+v", p.To)
	}
	if p, err = ctrl.SetPolicy(ctx, "daily", localapi.PolicyChange{To: []string{"all"}}); err != nil || len(p.To) != 0 {
		t.Errorf("all: %+v, %v", p, err)
	}
}

func TestBackupNowTagMustBeAPolicy(t *testing.T) {
	_, err := newController(t).BackupNow(context.Background(), "", []string{"yearly"})
	if errCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "not a policy") || !strings.Contains(err.Error(), "manual") {
		t.Errorf("err = %v", err)
	}
}

func TestBackupGoesOnlyToItsPolicyDestinations(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	pc1 := startAgent(t, "node", "PC-1")
	pairNode(t, ctrl, pc1)
	pairNode(t, ctrl, startAgent(t, "node", "PC-2"))
	if _, err := ctrl.SetPolicy(ctx, "daily", localapi.PolicyChange{To: []string{"PC-1"}}); err != nil {
		t.Fatal(err)
	}
	id := backupAt(t, ctrl, tick0, store.BackupDistributed, "daily")
	b, err := ctrl.Backup(ctx, id)
	if err != nil || len(b.Copies) != 1 || b.Copies[0].AgentID != pc1.id.AgentID {
		t.Errorf("copies %+v, %v", b.Copies, err)
	}
}

func TestRemoveAgent(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	pc1, pc2 := startAgent(t, "node", "PC-1"), startAgent(t, "node", "PC-2")
	pairNode(t, ctrl, pc1)
	pairNode(t, ctrl, pc2)
	ctrl.PollAll(ctx)

	if _, err := ctrl.SetPolicy(ctx, "daily", localapi.PolicyChange{To: []string{"PC-1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ctrl.RemoveAgent(ctx, "PC-1", true, "PC-1"); !is409(err, "policy daily sends only to PC-1") {
		t.Errorf("sole destination of a policy: %v", err)
	}
	if _, err := ctrl.SetPolicy(ctx, "daily", localapi.PolicyChange{To: []string{"PC-1", "PC-2"}}); err != nil {
		t.Fatal(err)
	}
	id := backupAt(t, ctrl, tick0, store.BackupDistributed, "daily")
	if err := ctrl.Store.MarkCopy(ctx, id, pc2.id.AgentID, store.CopyDeleted, "test", tick0); err != nil {
		t.Fatal(err)
	}
	if _, err := ctrl.RemoveAgent(ctx, "PC-1", false, ""); !is409(err, "1 backup has its only copy on PC-1") || !is409(err, "--force") {
		t.Errorf("only copy: %v", err)
	}

	a, err := ctrl.RemoveAgent(ctx, "PC-1", true, "PC-1")
	if err != nil || a.Hostname != "PC-1" {
		t.Fatalf("remove: %+v, %v", a, err)
	}
	as, _ := ctrl.Agents(ctx)
	if len(as) != 1 || as[0].Hostname != "PC-2" {
		t.Errorf("agents after remove %+v", as)
	}
	if _, ok := ctrl.pollSnapshot()[pc1.id.AgentID]; ok {
		t.Error("poll state kept for the removed node")
	}
	b, _ := ctrl.Backup(ctx, id)
	for _, c := range b.Copies {
		if c.AgentID == pc1.id.AgentID {
			t.Errorf("copy record kept: %+v", c)
		}
	}
	if p, _ := ctrl.Store.Policy(ctx, "daily"); !slices.Equal(p.Destinations, []string{pc2.id.AgentID}) {
		t.Errorf("daily destinations %q", p.Destinations)
	}
	if len(stored(t, pc1)) == 0 {
		t.Error("the node's files must stay")
	}
	if _, err := ctrl.RemoveAgent(ctx, "PC-1", true, "PC-1"); err == nil {
		t.Error("removing twice succeeded")
	}
}
