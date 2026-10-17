package coord

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/dump/dumptest"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/pairing"
	"github.com/jufianto/sevjala/store"
)

var (
	asUser  = localapi.WithAdmin(context.Background(), false)
	asAdmin = localapi.WithAdmin(context.Background(), true)
)

func TestAdminOnlyActions(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	pc1 := startAgent(t, "node", "PC-1")
	pairNode(t, ctrl, pc1)
	svc := &Service{Controller: ctrl}
	node := &Service{Node: &Node{AgentID: "n1", Codes: pairing.New(time.Now), Log: discard}}

	if _, err := ctrl.AddPolicy(context.Background(), localapi.PolicyChange{Name: "weekly", Keep: ptr(4)}); err != nil {
		t.Fatal(err)
	}
	backupAt(t, ctrl, tick0, store.BackupDistributed, "weekly")
	if err := svc.RemovePolicy(asUser, "weekly"); err != nil {
		t.Fatalf("removing a policy deletes nothing and needs no admin: %v", err)
	}
	if _, err := svc.SetPolicy(asAdmin, "daily", localapi.PolicyChange{To: []string{"PC-1"}}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		call  func(ctx context.Context) error
		admin bool
	}{
		{"pair", func(ctx context.Context) error { return second(svc.Pair(ctx, "PC-9", "123456", "")) }, true},
		{"pairing code on a node", func(ctx context.Context) error { return second(node.Code(ctx)) }, true},
		{"db set --dumper", func(ctx context.Context) error {
			return second(svc.SetDatabase(ctx, localapi.DBSettings{Dumper: ptr("/bin/sh")}))
		}, true},
		{"db set host", func(ctx context.Context) error {
			return second(svc.SetDatabase(ctx, localapi.DBSettings{Host: ptr("192.168.1.99"), Password: ptr("theirs")}))
		}, true},
		{"db set port", func(ctx context.Context) error {
			return second(svc.SetDatabase(ctx, localapi.DBSettings{Port: ptr(3307)}))
		}, true},
		{"db set user with its password", func(ctx context.Context) error {
			return second(svc.SetDatabase(ctx, localapi.DBSettings{User: ptr("backup"), Password: ptr("pw")}))
		}, false},
		{"db set databases", func(ctx context.Context) error {
			return second(svc.SetDatabase(ctx, localapi.DBSettings{Databases: []string{"shop"}}))
		}, false},
		{"policy add over an old tag", func(ctx context.Context) error {
			return second(svc.AddPolicy(ctx, localapi.PolicyChange{Name: "weekly", Keep: ptr(1)}))
		}, true},
		{"policy add, new tag", func(ctx context.Context) error {
			return second(svc.AddPolicy(ctx, localapi.PolicyChange{Name: "yearly", Keep: ptr(1)}))
		}, false},
		{"keep lowered", func(ctx context.Context) error {
			return second(svc.SetPolicy(ctx, "hourly", localapi.PolicyChange{Keep: ptr(3)}))
		}, true},
		{"keep raised", func(ctx context.Context) error {
			return second(svc.SetPolicy(ctx, "monthly", localapi.PolicyChange{Keep: ptr(20)}))
		}, false},
		{"every PC narrowed to one", func(ctx context.Context) error {
			return second(svc.SetPolicy(ctx, "frequent", localapi.PolicyChange{To: []string{"PC-1"}}))
		}, true},
		{"one PC widened to every PC", func(ctx context.Context) error {
			return second(svc.SetPolicy(ctx, "daily", localapi.PolicyChange{To: []string{"all"}}))
		}, false},
		{"schedule and enabled", func(ctx context.Context) error {
			return second(svc.SetPolicy(ctx, "monthly", localapi.PolicyChange{Schedule: ptr("0 2 1 * *"), Enabled: ptr(false)}))
		}, false},
		{"restore --file", func(ctx context.Context) error {
			return second(svc.Restore(ctx, localapi.RestoreRequest{File: "/nowhere/x.sql.gz", DryRun: true}))
		}, true},
		{"restore a backup", func(ctx context.Context) error {
			return second(svc.Restore(ctx, localapi.RestoreRequest{Backup: "latest", DryRun: true}))
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(asUser)
			if localapi.IsAdminRequired(err) != tt.admin {
				t.Fatalf("as a normal user: %v", err)
			}
			if tt.admin && (errCode(err) != http.StatusForbidden || !strings.Contains(err.Error(), "needs administrator rights")) {
				t.Errorf("message: %v", err)
			}
			if err := tt.call(asAdmin); localapi.IsAdminRequired(err) {
				t.Errorf("as admin: %v", err)
			}
		})
	}
}

func TestChangingTheDatabaseHostNeedsThePassword(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	pw := "secret"
	if _, err := ctrl.SetDatabase(context.Background(), localapi.DBSettings{Password: &pw}); err != nil {
		t.Fatal(err)
	}
	host := ctrl.config().Controller.Database.Host
	for _, s := range []localapi.DBSettings{{Host: ptr("192.168.1.99")}, {User: ptr("someone")}} {
		if _, err := ctrl.SetDatabase(context.Background(), s); errCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "password") {
			t.Errorf("%+v: %v", s, err)
		}
	}
	if _, err := ctrl.SetDatabase(context.Background(), localapi.DBSettings{Host: &host, Databases: []string{"shop"}}); err != nil {
		t.Errorf("the same host again: %v", err)
	}
	if _, err := ctrl.SetDatabase(context.Background(), localapi.DBSettings{Host: ptr("127.0.0.1"), Password: &pw}); err != nil {
		t.Errorf("a new host with its password: %v", err)
	}
}
