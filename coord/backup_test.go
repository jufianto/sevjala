package coord

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/dump"
	"github.com/jufianto/sevjala/dump/dumptest"
	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/store"
)

func pairNode(t *testing.T, ctrl *Controller, n testAgent) {
	t.Helper()
	ctx := context.Background()
	if _, err := ctrl.AddAgent(ctx, n.lan.Addr()); err != nil {
		t.Fatal(err)
	}
	code, _, _ := n.codes.Issue()
	if _, err := ctrl.Pair(ctx, n.id.AgentID, code, n.id.FP); err != nil {
		t.Fatal(err)
	}
}

func waitState(t *testing.T, ctrl *Controller, id string, done func(localapi.Backup) bool) localapi.Backup {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		b, err := ctrl.Backup(context.Background(), id)
		if err == nil && done(b) {
			return b
		}
		if time.Now().After(deadline) {
			t.Fatalf("backup %s: %+v, %v", id, b, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func state(s string) func(localapi.Backup) bool {
	return func(b localapi.Backup) bool { return b.State == s }
}

func errCode(err error) int {
	var e *localapi.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

func TestBackupNowReachesThreeNodes(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	var nodes []testAgent
	for _, h := range []string{"PC-1", "PC-2", "PC-3"} {
		n := startAgent(t, "node", h)
		pairNode(t, ctrl, n)
		nodes = append(nodes, n)
	}
	jobs, err := ctrl.BackupNow(context.Background(), "", nil)
	if err != nil || len(jobs) != 1 || jobs[0].Database != "shop" {
		t.Fatalf("jobs %+v, %v", jobs, err)
	}
	b := waitState(t, ctrl, jobs[0].BackupID, state(store.BackupDistributed))
	if len(b.Copies) != 3 || !slices.Equal(b.Tags, []string{"manual"}) {
		t.Fatalf("backup %+v", b)
	}
	for _, c := range b.Copies {
		if c.State != store.CopyVerified || c.VerifiedAt.IsZero() || !strings.HasPrefix(c.Hostname, "PC-") {
			t.Errorf("copy %+v", c)
		}
	}
	for _, n := range nodes {
		inv, _ := n.inv.Scan()
		if len(inv.Backups) != 1 || inv.Backups[0].SHA256 != b.SHA256 || inv.Backups[0].State != inventory.StateVerified {
			t.Errorf("node store %+v", inv)
		}
	}
	for _, ext := range []string{inventory.DataExt, inventory.ManifestExt} {
		if _, err := os.Stat(filepath.Join(ctrl.Staging, b.BackupID+ext)); err != nil {
			t.Errorf("staging: %v", err)
		}
	}
}

func TestBackupNowWithoutNodesStaysInStaging(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	jobs, err := ctrl.BackupNow(context.Background(), "shop", []string{"daily"})
	if err != nil {
		t.Fatal(err)
	}
	b := waitState(t, ctrl, jobs[0].BackupID, state(store.BackupCreated))
	if len(b.Copies) != 0 || !slices.Equal(b.Tags, []string{"daily"}) {
		t.Errorf("backup %+v", b)
	}
}

func TestBackupNowRefuses(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	tests := []struct {
		name string
		dbs  []string
		db   string
		tags []string
		want string
	}{
		{"nothing configured", nil, "", nil, "no database is configured"},
		{"unknown database", []string{"shop"}, "other", nil, "not a configured database"},
		{"bad tag", []string{"shop"}, "", []string{"Daily Run"}, "tag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := newController(t)
			ctrl.Config.Controller.Database.Databases = tt.dbs
			_, err := ctrl.BackupNow(context.Background(), tt.db, tt.tags)
			if errCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestBackupNowWhileDumpingIs409(t *testing.T) {
	dumptest.Use(t, dumptest.Hang)
	ctrl := newController(t)
	jobs, err := ctrl.BackupNow(context.Background(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, ctrl, jobs[0].BackupID, state(localapi.BackupDumping))
	if _, err := ctrl.BackupNow(context.Background(), "", nil); errCode(err) != http.StatusConflict {
		t.Errorf("err = %v", err)
	}
}

func TestBackupDumpFailureIsReported(t *testing.T) {
	dumptest.Use(t, dumptest.DumpFails)
	ctrl := newController(t)
	jobs, _ := ctrl.BackupNow(context.Background(), "", nil)
	b := waitState(t, ctrl, jobs[0].BackupID, state(localapi.BackupFailed))
	if !strings.Contains(b.Error, "Unknown database") {
		t.Errorf("error %q", b.Error)
	}
	if all, _ := ctrl.Backups(context.Background()); len(all) != 1 || all[0].State != localapi.BackupFailed {
		t.Errorf("list %+v", all)
	}
}

func TestPushToStoppedNodeEndsOffline(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	n := startAgent(t, "node", "PC-OFF")
	pairNode(t, ctrl, n)
	n.lan.Shutdown(context.Background())
	jobs, _ := ctrl.BackupNow(context.Background(), "", nil)
	b := waitState(t, ctrl, jobs[0].BackupID, state(store.BackupPartial))
	if c := b.Copies[0]; c.State != store.CopyOffline || c.Attempts != 4 || c.LastError == "" {
		t.Errorf("copy %+v", c)
	}
}

// A node re-paired to another controller refuses us at TLS; retrying for
// twelve minutes cannot fix that.
func TestPushToRepairedNodeFailsAtOnce(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	n := startAgent(t, "node", "PC-MOVED")
	pairNode(t, ctrl, n)
	pairNode(t, newController(t), n)
	jobs, _ := ctrl.BackupNow(context.Background(), "", nil)
	b := waitState(t, ctrl, jobs[0].BackupID, state(store.BackupPartial))
	if c := b.Copies[0]; c.State != store.CopyFailed || c.Attempts != 1 {
		t.Errorf("copy %+v", c)
	}
}

func TestResumeRequeuesUnsettled(t *testing.T) {
	bin, _ := dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	n := startAgent(t, "node", "PC-R")
	pairNode(t, ctrl, n)
	ctx := context.Background()
	m, err := dump.Run(ctx, dump.Options{ID: inventory.NewID(time.Now()), Database: "shop", StagingDir: ctrl.Staging,
		SourceAgent: ctrl.Self.AgentID, Created: time.Now(), Tools: dump.Tools{Dumper: bin, Client: bin},
		Conn: dump.Conn{Host: "127.0.0.1", Port: 3306, User: "root"}})
	if err != nil {
		t.Fatal(err)
	}
	b := store.Backup{BackupID: m.BackupID, Database: m.Database, CreatedAt: m.CreatedAt, Size: m.Size, SHA256: m.SHA256,
		StagingPath: m.BackupID + inventory.DataExt}
	if err := ctrl.Store.InsertBackup(ctx, b, []string{n.id.AgentID}); err != nil {
		t.Fatal(err)
	}
	_ = ctrl.Store.SetCopy(ctx, store.Copy{BackupID: m.BackupID, AgentID: n.id.AgentID, State: store.CopyTransferring, Attempts: 1})
	if err := ctrl.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	waitState(t, ctrl, m.BackupID, state(store.BackupDistributed))
}

func TestSetDatabaseSavesWithoutThePassword(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	pw, dbs := "p a$s\"wörd", []string{"shop", "legacy"}
	d, err := ctrl.SetDatabase(context.Background(), localapi.DBSettings{Password: &pw, Databases: dbs})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Databases) != 2 || !d.Databases[0].Configured || !d.Databases[1].Configured || d.Databases[0].Consistency != "--lock-tables" {
		t.Errorf("detection %+v", d)
	}
	raw, err := os.ReadFile(config.Path(ctrl.DataDir))
	if err != nil || strings.Contains(string(raw), "wörd") || !strings.Contains(string(raw), `password_ref = "keyring:db"`) {
		t.Errorf("config.toml %q, %v", raw, err)
	}
	if got, err := config.Secret(ctrl.DataDir, "keyring:db"); err != nil || got != pw {
		t.Errorf("secret %q, %v", got, err)
	}
	empty := ""
	if _, err := ctrl.SetDatabase(context.Background(), localapi.DBSettings{Password: &empty}); err != nil {
		t.Fatal(err)
	}
	if ctrl.config().Controller.Database.PasswordRef != "" {
		t.Error("password_ref kept after clearing the password")
	}
}

func TestSetDatabaseRefusesBeforeSaving(t *testing.T) {
	tests := []struct {
		name, mode string
		s          localapi.DBSettings
		want       string
	}{
		{"unknown database", dumptest.OK, localapi.DBSettings{Databases: []string{"nope"}}, "does not exist"},
		{"login denied", dumptest.Denied, localapi.DBSettings{Databases: []string{"shop"}}, "Access denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dumptest.Use(t, tt.mode)
			ctrl := newController(t)
			if _, err := ctrl.SetDatabase(context.Background(), tt.s); errCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v", err)
			}
			if _, err := os.Stat(config.Path(ctrl.DataDir)); !errors.Is(err, os.ErrNotExist) {
				t.Error("config written")
			}
		})
	}
}
