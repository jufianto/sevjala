package coord

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
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
	deadline := time.Now().Add(30 * time.Second)
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
	for name, damage := range map[string]func([]byte) []byte{
		"bytes changed": func(raw []byte) []byte { raw[len(raw)/2] ^= 0xff; return raw },
		"truncated":     func(raw []byte) []byte { return raw[:len(raw)/2] },
	} {
		t.Run(name, func(t *testing.T) { restoreSkipsDamaged(t, damage) })
	}
}

func restoreSkipsDamaged(t *testing.T, damage func([]byte) []byte) {
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
	os.WriteFile(path, damage(raw), 0o600)
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
	dumptest.Use(t, dumptest.OK)
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

func TestRestoreReplaceProduction(t *testing.T) {
	_, log := dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	b := backupOne(t, ctrl, store.BackupCreated)
	if _, err := ctrl.Restore(ctx, localapi.RestoreRequest{Backup: b.BackupID, Replace: true, Confirm: "shop"}); err != nil {
		t.Fatal(err)
	}
	r := waitRestore(t, ctrl)
	if r.State != localapi.RestoreDone || !r.Replace || r.Target != "shop" || r.PreRestoreID == "" || r.PreRestoreID == b.BackupID {
		t.Fatalf("restore %+v", r)
	}
	safety, err := ctrl.Store.Backup(ctx, r.PreRestoreID)
	if err != nil || safety.Database != "shop" || !slices.Equal(safety.Tags, []string{"pre-restore"}) {
		t.Errorf("pre-restore backup %+v, %v", safety, err)
	}
	calls := strings.Split(fakeLog(t, log), "\n")
	drop := -1
	for i, c := range calls {
		if strings.Contains(c, "-e DROP DATABASE `shop`") {
			drop = i
		}
	}
	dumps := 0
	for _, c := range calls[:max(drop, 0)] {
		if strings.Contains(c, "--routines") {
			dumps++
		}
	}
	if drop < 0 || dumps != 2 {
		t.Errorf("want the drop after two dumps (the backup, then the pre-restore one):\n%s", strings.Join(calls, "\n"))
	}
	rest := strings.Join(calls[max(drop, 0):], "\n")
	for _, want := range []string{"CREATE DATABASE `shop` CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci", "LOAD shop "} {
		if !strings.Contains(rest, want) {
			t.Errorf("no %q after the drop in\n%s", want, rest)
		}
	}
}

func TestRestoreReplaceFailureNamesTheWayBack(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	b := backupOne(t, ctrl, store.BackupCreated)
	dumptest.Use(t, dumptest.LoadFails)
	if _, err := ctrl.Restore(context.Background(), localapi.RestoreRequest{Backup: b.BackupID, Replace: true, Confirm: "shop"}); err != nil {
		t.Fatal(err)
	}
	r := waitRestore(t, ctrl)
	if want := "sevjala restore " + r.PreRestoreID + " --replace-production"; r.State != localapi.RestoreFailed ||
		r.PreRestoreID == "" || !strings.Contains(r.Error, want) {
		t.Errorf("restore %+v, want %q in the error", r, want)
	}
}

func TestRestoreReplaceFailedSafetyBackupLeavesProduction(t *testing.T) {
	_, log := dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	b := backupOne(t, ctrl, store.BackupCreated)
	dumptest.Use(t, dumptest.DumpFails)
	if _, err := ctrl.Restore(context.Background(), localapi.RestoreRequest{Backup: b.BackupID, Replace: true, Confirm: "shop"}); err != nil {
		t.Fatal(err)
	}
	r := waitRestore(t, ctrl)
	if r.State != localapi.RestoreFailed || !strings.Contains(r.Error, "shop was not touched") {
		t.Errorf("restore %+v", r)
	}
	if strings.Contains(fakeLog(t, log), "DROP DATABASE") {
		t.Error("production dropped although the pre-restore backup failed")
	}
}

func TestRestoreReplaceHoldsBackups(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	b := backupOne(t, ctrl, store.BackupCreated)
	dumptest.Use(t, dumptest.LoadHangs)
	if _, err := ctrl.Restore(ctx, localapi.RestoreRequest{Backup: b.BackupID, Replace: true, Confirm: "shop"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for r, _ := ctrl.RestoreStatus(ctx); !strings.HasPrefix(r.Step, "loading"); r, _ = ctrl.RestoreStatus(ctx) {
		if time.Now().After(deadline) || r.State != localapi.RestoreRunning {
			t.Fatalf("never loading: %+v", r)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := ctrl.BackupNow(ctx, "shop", nil); apiCode(err) != http.StatusConflict || !strings.Contains(err.Error(), "loaded by a restore") {
		t.Errorf("backup during the replace: %v", err)
	}
}

func TestRestoreReplaceRefuses(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	b := backupOne(t, ctrl, store.BackupCreated)
	crm := "BK-20261016-101500-C4A1"
	if err := ctrl.Store.InsertBackup(ctx, store.Backup{BackupID: crm, Database: "crm", CreatedAt: time.Now(), Size: 1,
		SHA256: strings.Repeat("0", 64), Tags: []string{"manual"}, StagingPath: crm + inventory.DataExt}, nil); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		req  localapi.RestoreRequest
		code int
	}{
		{localapi.RestoreRequest{Backup: b.BackupID, Replace: true}, http.StatusConflict},
		{localapi.RestoreRequest{Backup: b.BackupID, Replace: true, Confirm: "Shop"}, http.StatusConflict},
		{localapi.RestoreRequest{Backup: b.BackupID, Replace: true, Confirm: "shop", Into: "x"}, http.StatusBadRequest},
		{localapi.RestoreRequest{Backup: crm, Replace: true, Confirm: "crm"}, http.StatusConflict},
	} {
		if _, err := ctrl.Restore(ctx, tt.req); apiCode(err) != tt.code {
			t.Errorf("%+v: err = %v, want %d", tt.req, err, tt.code)
		}
	}
	dumptest.Use(t, dumptest.Hang)
	if _, err := ctrl.BackupNow(ctx, "shop", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ctrl.Restore(ctx, localapi.RestoreRequest{Backup: b.BackupID, Replace: true, Confirm: "shop"}); apiCode(err) != http.StatusConflict {
		t.Errorf("replace while shop is dumping: %v", err)
	}
}

// exported copies b's staging files to dir the way 'backups export' names them.
func exported(t *testing.T, ctrl *Controller, id, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "2026-10-16_10-00-00"+inventory.DataExt)
	for _, ext := range []string{inventory.DataExt, inventory.ManifestExt} {
		raw, err := os.ReadFile(filepath.Join(ctrl.Staging, id+ext))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(strings.TrimSuffix(path, inventory.DataExt)+ext, raw, 0o600)
	}
	return path
}

func TestRestoreFromFile(t *testing.T) {
	_, log := dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	b := backupOne(t, ctrl, store.BackupCreated)
	path := exported(t, ctrl, b.BackupID, t.TempDir())
	os.Remove(filepath.Join(ctrl.Staging, b.BackupID+inventory.DataExt))
	if _, err := ctrl.Restore(ctx, localapi.RestoreRequest{File: path, Into: "shop_file"}); err != nil {
		t.Fatal(err)
	}
	r := waitRestore(t, ctrl)
	if r.State != localapi.RestoreDone || r.Source != path || r.BackupID != b.BackupID || r.Database != "shop" {
		t.Fatalf("restore %+v", r)
	}
	if !strings.Contains(fakeLog(t, log), "LOAD shop_file ") {
		t.Error("nothing loaded")
	}
}

func TestRestoreFromDamagedFileMarksNothing(t *testing.T) {
	_, log := dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	b := backupOne(t, ctrl, store.BackupCreated)
	path := exported(t, ctrl, b.BackupID, t.TempDir())
	raw, _ := os.ReadFile(path)
	raw[len(raw)/2] ^= 0xff
	os.WriteFile(path, raw, 0o600)
	if _, err := ctrl.Restore(ctx, localapi.RestoreRequest{File: path, Into: "shop_file"}); err != nil {
		t.Fatal(err)
	}
	r := waitRestore(t, ctrl)
	if r.State != localapi.RestoreFailed || !strings.Contains(r.Error, "damaged") {
		t.Errorf("restore %+v", r)
	}
	if got, err := ctrl.Store.Backup(ctx, b.BackupID); err != nil || got.StagingPath == "" {
		t.Errorf("the controller's own copy must stay: %+v, %v", got, err)
	}
	if strings.Contains(fakeLog(t, log), "CREATE DATABASE") {
		t.Error("a damaged file must not reach the database")
	}
}

func TestRestoreFileRefuses(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	b := backupOne(t, ctrl, store.BackupCreated)
	dir := t.TempDir()
	path := exported(t, ctrl, b.BackupID, dir)
	bare := filepath.Join(dir, "bare"+inventory.DataExt)
	os.WriteFile(bare, []byte("x"), 0o600)
	for _, req := range []localapi.RestoreRequest{
		{File: "shop.sql.gz"},
		{File: filepath.Join(dir, "missing.sql.gz")},
		{File: bare},
		{File: dir},
		{File: path, Backup: b.BackupID},
	} {
		if _, err := ctrl.Restore(ctx, req); apiCode(err) != http.StatusBadRequest {
			t.Errorf("%+v: err = %v, want 400", req, err)
		}
	}
}

func TestRestoreDryRunStartsNothing(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	b := backupOne(t, ctrl, store.BackupCreated)
	v, err := ctrl.Restore(ctx, localapi.RestoreRequest{Backup: "latest", Replace: true, DryRun: true})
	if err != nil || v.State != "" || v.Target != "shop" || v.BackupID != b.BackupID || !v.BackupCreatedAt.Equal(b.CreatedAt) || v.Size != b.Size {
		t.Errorf("dry run %+v, %v", v, err)
	}
	if _, err := ctrl.RestoreStatus(ctx); apiCode(err) != http.StatusNotFound {
		t.Errorf("a dry run started a restore: %v", err)
	}
	if _, err := ctrl.Restore(ctx, localapi.RestoreRequest{Backup: b.BackupID, Into: "shop", DryRun: true}); apiCode(err) != http.StatusConflict {
		t.Errorf("dry run into an existing database: %v", err)
	}
}

// waitLoading returns once the running restore has reached its load step.
func waitLoading(t *testing.T, ctrl *Controller) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for r, _ := ctrl.RestoreStatus(context.Background()); !strings.HasPrefix(r.Step, "loading"); r, _ = ctrl.RestoreStatus(context.Background()) {
		if time.Now().After(deadline) || (r.State != "" && r.State != localapi.RestoreRunning) {
			t.Fatalf("never loading: %+v", r)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRestoreIntoAConfiguredNameHoldsItsBackups(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	b := backupOne(t, ctrl, store.BackupCreated)
	ctrl.Config.Controller.Database.Databases = []string{"shop", "crm"}
	dumptest.Use(t, dumptest.LoadHangs)
	if _, err := ctrl.Restore(ctx, localapi.RestoreRequest{Backup: b.BackupID, Into: "crm"}); err != nil {
		t.Fatal(err)
	}
	waitLoading(t, ctrl)
	if _, err := ctrl.BackupNow(ctx, "crm", nil); apiCode(err) != http.StatusConflict {
		t.Errorf("backup of a database being loaded: %v", err)
	}
}

func TestRestoreReplaceFailureKeepsBackupsHeldUntilTheWayBackSucceeds(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	b := backupOne(t, ctrl, store.BackupCreated)
	dumptest.Use(t, dumptest.LoadFails)
	if _, err := ctrl.Restore(ctx, localapi.RestoreRequest{Backup: b.BackupID, Replace: true, Confirm: "shop"}); err != nil {
		t.Fatal(err)
	}
	failed := waitRestore(t, ctrl)
	ctrl.mu.Lock()
	ctrl.replacing = nil // as after a service restart
	ctrl.mu.Unlock()
	way := "sevjala restore " + failed.PreRestoreID + " --replace-production"
	if _, err := ctrl.BackupNow(ctx, "shop", nil); apiCode(err) != http.StatusConflict || !strings.Contains(err.Error(), way) {
		t.Fatalf("backup of a partly loaded database: %v", err)
	}

	_, log := dumptest.Use(t, dumptest.OK)
	if _, err := ctrl.Restore(ctx, localapi.RestoreRequest{Backup: failed.PreRestoreID, Replace: true, Confirm: "shop"}); err != nil {
		t.Fatal(err)
	}
	r := waitRestore(t, ctrl)
	if r.State != localapi.RestoreDone || r.PreRestoreID != failed.PreRestoreID {
		t.Fatalf("way back %+v", r)
	}
	if strings.Contains(fakeLog(t, log), "--routines") {
		t.Error("the way back dumped the partly loaded database again")
	}
	if _, err := ctrl.BackupNow(ctx, "shop", nil); err != nil {
		t.Errorf("backups after the way back: %v", err)
	}
}

func TestRestoreEndsWithTheAgent(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	b := backupOne(t, ctrl, store.BackupCreated)
	ctx, cancel := context.WithCancel(context.Background())
	ctrl.Ctx = ctx
	dumptest.Use(t, dumptest.LoadHangs)
	if _, err := ctrl.Restore(context.Background(), localapi.RestoreRequest{Backup: b.BackupID, Into: "shop_x"}); err != nil {
		t.Fatal(err)
	}
	waitLoading(t, ctrl)
	cancel()
	if !ctrl.WaitRestore(10 * time.Second) {
		t.Fatal("restore still running after the agent stopped")
	}
	if r, _ := ctrl.RestoreStatus(context.Background()); r.State != localapi.RestoreFailed || !strings.Contains(r.Error, "shop_x was removed") {
		t.Errorf("restore %+v", r)
	}
}

func TestRestoreFromFileLoadsACheckedCopy(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	b := backupOne(t, ctrl, store.BackupCreated)
	path := exported(t, ctrl, b.BackupID, t.TempDir())
	dumptest.Use(t, dumptest.LoadHangs)
	if _, err := ctrl.Restore(context.Background(), localapi.RestoreRequest{File: path, Into: "shop_file"}); err != nil {
		t.Fatal(err)
	}
	waitLoading(t, ctrl)
	if copies, _ := filepath.Glob(filepath.Join(ctrl.Staging, "*.restore.part")); len(copies) != 1 {
		t.Errorf("the file must be loaded from the copy checked into the controller's own folder, got %v", copies)
	}
}

func TestRestoreLatestSkipsBackupsOnlyOnOfflineNodes(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	node := startAgent(t, "node", "PC-1")
	pairNode(t, ctrl, node)
	older := backupOne(t, ctrl, store.BackupDistributed)
	time.Sleep(1100 * time.Millisecond) // a new id second
	newer := backupOne(t, ctrl, store.BackupDistributed)
	for _, ext := range []string{inventory.DataExt, inventory.ManifestExt} {
		os.Remove(filepath.Join(ctrl.Staging, newer.BackupID+ext))
	}
	if err := ctrl.Store.Unstage(ctx, newer.BackupID); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.Store.SetAgentState(ctx, node.id.AgentID, store.AgentOffline); err != nil {
		t.Fatal(err)
	}
	if v, err := ctrl.Restore(ctx, localapi.RestoreRequest{Backup: "latest", DryRun: true}); err != nil || v.BackupID != older.BackupID {
		t.Errorf("latest = %s, %v; want %s (the newer one is only on an offline node)", v.BackupID, err, older.BackupID)
	}
}
