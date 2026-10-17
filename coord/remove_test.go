package coord

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jufianto/sevjala/dump/dumptest"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/store"
)

func TestLastCopies(t *testing.T) {
	b := func(id, db, state string, copies ...store.Copy) store.Backup {
		return store.Backup{BackupID: id, Database: db, State: state, Copies: copies}
	}
	ok := func(agent string) store.Copy { return store.Copy{AgentID: agent, State: store.CopyVerified} }
	bad := func(agent string) store.Copy { return store.Copy{AgentID: agent, State: store.CopyCorrupt} }
	staged := func(id string) bool { return id == "S" }
	dbs := []string{"shop", "hr"}
	tests := []struct {
		name string
		bs   []store.Backup
		want []string
	}{
		{"only on A", []store.Backup{b("1", "shop", store.BackupDistributed, ok("A"))}, []string{"shop"}},
		{"also on B", []store.Backup{b("1", "shop", store.BackupDistributed, ok("A")), b("2", "shop", store.BackupDistributed, ok("B"))}, nil},
		{"B's copy is damaged", []store.Backup{b("1", "shop", store.BackupDistributed, ok("A"), bad("B"))}, []string{"shop"}},
		{"the controller's own copy counts", []store.Backup{b("1", "shop", store.BackupDistributed, ok("A")), b("S", "shop", store.BackupCreated)}, nil},
		{"an expired backup elsewhere does not", []store.Backup{b("1", "shop", store.BackupDistributed, ok("A")), b("2", "shop", store.BackupExpired, ok("B"))}, []string{"shop"}},
		{"a database not configured", []store.Backup{b("1", "legacy", store.BackupDistributed, ok("A"))}, nil},
		{"two databases", []store.Backup{b("1", "shop", store.BackupDistributed, ok("A")), b("2", "hr", store.BackupDistributed, ok("A"))}, []string{"shop", "hr"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lastCopies(tt.bs, "A", staged, dbs); !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestForcedRemovalNeedsTheHostname(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	pc1, pc2 := startAgent(t, "node", "PC-1"), startAgent(t, "node", "PC-2")
	pairNode(t, ctrl, pc1)
	pairNode(t, ctrl, pc2)
	id := backupAt(t, ctrl, tick0, store.BackupDistributed, "daily")
	if err := ctrl.Store.MarkCopy(ctx, id, pc2.id.AgentID, store.CopyDeleted, "test", tick0); err != nil {
		t.Fatal(err)
	}
	_, err := ctrl.RemoveAgent(ctx, "PC-1", true, "")
	if e, ok := localapi.AsConfirm(err); !ok || e.Word != "PC-1" || e.Deletes != 1 || !slices.Equal(e.Hosts, []string{"PC-1"}) {
		t.Fatalf("force without the hostname: %v", err)
	}
	if _, err := ctrl.RemoveAgent(ctx, "PC-1", true, "pc-1"); err == nil {
		t.Fatal("a different word removed it")
	}
	if a, err := ctrl.RemoveAgent(ctx, "PC-1", true, "PC-1"); err != nil || a.Hostname != "PC-1" {
		t.Errorf("confirmed: %+v, %v", a, err)
	}
}

func TestRemovalKeepsTheLastGoodCopy(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	pc1 := startAgent(t, "node", "PC-1")
	pairNode(t, ctrl, pc1)
	id := backupAt(t, ctrl, tick0, store.BackupDistributed, "daily")
	if err := ctrl.unstage(id); err != nil {
		t.Fatal(err)
	}
	for _, confirm := range []string{"", "PC-1"} {
		_, err := ctrl.RemoveAgent(ctx, "PC-1", true, confirm)
		if errCode(err) != 409 || !strings.Contains(err.Error(), "only verified copy of shop") {
			t.Errorf("confirm %q: %v", confirm, err)
		}
	}
}
