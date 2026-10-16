package coord

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/dump"
	"github.com/jufianto/sevjala/dump/dumptest"
	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/store"
)

// backupOne makes one backup of shop and waits until it settles.
func backupOne(t *testing.T, ctrl *Controller, want string) localapi.Backup {
	t.Helper()
	jobs, err := ctrl.BackupNow(context.Background(), "shop", nil)
	if err != nil {
		t.Fatal(err)
	}
	return waitState(t, ctrl, jobs[0].BackupID, state(want))
}

func waitRestore(t *testing.T, ctrl *Controller) localapi.Restore {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		r, err := ctrl.RestoreStatus(context.Background())
		if err == nil && r.State != localapi.RestoreRunning {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("restore still running: %+v, %v", r, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func fakeLog(t *testing.T, log string) string {
	t.Helper()
	raw, _ := os.ReadFile(log)
	return string(raw)
}

func apiCode(err error) int {
	var e *localapi.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

func TestRestoreFromOwnCopy(t *testing.T) {
	_, log := dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	b := backupOne(t, ctrl, store.BackupCreated)
	started, err := ctrl.Restore(context.Background(), localapi.RestoreRequest{Backup: b.BackupID})
	if err != nil {
		t.Fatal(err)
	}
	target := dump.DefaultTarget("shop", b.CreatedAt)
	if started.State != localapi.RestoreRunning || started.Target != target || started.BackupID != b.BackupID {
		t.Errorf("started %+v", started)
	}
	r := waitRestore(t, ctrl)
	if r.State != localapi.RestoreDone || r.Source != "the controller's own copy" || r.Tables != 2 || r.FinishedAt.IsZero() {
		t.Fatalf("restore %+v", r)
	}
	calls := fakeLog(t, log)
	for _, want := range []string{"CREATE DATABASE `" + target + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci",
		"LOAD " + target + " " + strconv.Itoa(len(dumptest.Dump("shop")))} {
		if !strings.Contains(calls, want) {
			t.Errorf("no %q in\n%s", want, calls)
		}
	}
}

func TestRestoreFetchesFromANodeAndSkipsADamagedCopy(t *testing.T) {
	_, log := dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	good, bad := startAgent(t, "node", "PC-1"), startAgent(t, "node", "PC-2")
	pairNode(t, ctrl, good)
	pairNode(t, ctrl, bad)
	b := backupOne(t, ctrl, store.BackupDistributed)
	for _, ext := range []string{inventory.DataExt, inventory.ManifestExt} {
		os.Remove(filepath.Join(ctrl.Staging, b.BackupID+ext))
	}
	if err := ctrl.Store.Unstage(ctx, b.BackupID); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bad.inv.Dir(), b.BackupID+inventory.DataExt)
	raw, _ := os.ReadFile(path)
	raw[len(raw)/2] ^= 0xff
	os.WriteFile(path, raw, 0o600)
	if err := ctrl.Store.MarkCopy(ctx, b.BackupID, bad.id.AgentID, store.CopyVerified, "", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	ctrl.PollAll(ctx)

	if _, err := ctrl.Restore(ctx, localapi.RestoreRequest{Backup: "latest", Into: "shop_copy"}); err != nil {
		t.Fatal(err)
	}
	r := waitRestore(t, ctrl)
	if r.State != localapi.RestoreDone || r.Source != "PC-1" || r.Target != "shop_copy" {
		t.Fatalf("restore %+v", r)
	}
	got, _ := ctrl.Store.Backup(ctx, b.BackupID)
	for _, c := range got.Copies {
		if want := map[string]string{good.id.AgentID: store.CopyVerified, bad.id.AgentID: store.CopyCorrupt}[c.AgentID]; c.State != want {
			t.Errorf("copy on %s is %s, want %s", c.AgentID, c.State, want)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(ctrl.Staging, "*.restore.part")); len(left) != 0 {
		t.Errorf("left %v", left)
	}
	if !strings.Contains(fakeLog(t, log), "LOAD shop_copy ") {
		t.Error("nothing loaded")
	}
}

func TestRestoreLoadFailureDropsTheNewDatabase(t *testing.T) {
	_, log := dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	b := backupOne(t, ctrl, store.BackupCreated)
	dumptest.Use(t, dumptest.LoadFails)
	if _, err := ctrl.Restore(context.Background(), localapi.RestoreRequest{Backup: b.BackupID, Into: "shop_try"}); err != nil {
		t.Fatal(err)
	}
	r := waitRestore(t, ctrl)
	if r.State != localapi.RestoreFailed || !strings.Contains(r.Error, "Duplicate entry") || !strings.Contains(r.Error, "shop_try was removed") {
		t.Errorf("restore %+v", r)
	}
	_ = log
}

func TestRestoreRefuses(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	if _, err := ctrl.RestoreStatus(ctx); apiCode(err) != http.StatusNotFound {
		t.Errorf("status before any restore: %v", err)
	}
	if _, err := ctrl.Restore(ctx, localapi.RestoreRequest{Backup: "latest"}); apiCode(err) != http.StatusNotFound {
		t.Errorf("latest with no backups: %v", err)
	}
	b := backupOne(t, ctrl, store.BackupCreated)
	for _, tt := range []struct {
		req  localapi.RestoreRequest
		code int
	}{
		{localapi.RestoreRequest{Backup: "BK-20200101-000000-AAAA"}, http.StatusNotFound},
		{localapi.RestoreRequest{Backup: b.BackupID, Into: "a b"}, http.StatusBadRequest},
		{localapi.RestoreRequest{Backup: b.BackupID, Into: "shop"}, http.StatusConflict},
		{localapi.RestoreRequest{Backup: "latest", DB: "crm"}, http.StatusNotFound},
	} {
		if _, err := ctrl.Restore(ctx, tt.req); apiCode(err) != tt.code {
			t.Errorf("%+v: err = %v, want %d", tt.req, err, tt.code)
		}
	}
	ctrl.Config.Controller.Database.Databases = []string{"shop", "legacy"}
	if _, err := ctrl.Restore(ctx, localapi.RestoreRequest{Backup: "latest"}); apiCode(err) != http.StatusBadRequest ||
		!strings.Contains(err.Error(), "--db") {
		t.Errorf("latest with two databases: %v", err)
	}

	dumptest.Use(t, dumptest.Hang)
	if _, err := ctrl.Restore(ctx, localapi.RestoreRequest{Backup: b.BackupID}); err != nil {
		t.Fatal(err)
	}
	if _, err := ctrl.Restore(ctx, localapi.RestoreRequest{Backup: b.BackupID, Into: "other"}); apiCode(err) != http.StatusConflict {
		t.Errorf("second restore: %v", err)
	}
	in, _, _, err := ctrl.retentionInput(ctx)
	if err != nil || !in.Busy[b.BackupID] {
		t.Errorf("the backup being restored must be busy for retention: %v %v", in.Busy, err)
	}
}
