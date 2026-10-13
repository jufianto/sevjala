package coord

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jufianto/sevjala/dump/dumptest"
	"github.com/jufianto/sevjala/localapi"
)

// settled waits until the controller lists n backups and none is still dumping.
func settled(t *testing.T, ctrl *Controller, n int) []localapi.Backup {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		bs, err := ctrl.Backups(context.Background(), true)
		if err == nil && len(bs) == n && !slices.ContainsFunc(bs, func(b localapi.Backup) bool { return b.State == localapi.BackupDumping }) {
			return bs
		}
		if time.Now().After(deadline) {
			t.Fatalf("backups %+v, %v; want %d settled", bs, err, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

var tick0 = time.Date(2026, 1, 13, 10, 15, 0, 0, time.UTC) // a Tuesday before the real clock

func TestTickFiresDuePolicies(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	ctrl.Tick(ctx, tick0)
	bs := settled(t, ctrl, 1)
	if !slices.Equal(bs[0].Tags, []string{"daily", "frequent", "hourly", "monthly"}) || !bs[0].CreatedAt.Equal(tick0) {
		t.Fatalf("first tick %+v", bs[0])
	}
	ctrl.Tick(ctx, tick0.Add(time.Minute))
	settled(t, ctrl, 1)
	ctrl.Tick(ctx, tick0.Add(10*time.Minute))
	bs = settled(t, ctrl, 2)
	if !slices.Equal(bs[0].Tags, []string{"frequent"}) {
		t.Errorf("second backup %+v", bs[0])
	}
}

func TestTickSkipsWhileRunning(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	ctrl.mu.Lock()
	ctrl.jobs, ctrl.running = map[string]*job{}, map[string]string{"shop": "BK-20261013-101400-0001"}
	ctrl.mu.Unlock()
	ctrl.Tick(context.Background(), tick0)
	if bs, _ := ctrl.Backups(context.Background(), true); len(bs) != 0 {
		t.Errorf("backups %+v", bs)
	}
}

func TestTickWaitsAfterAFailedDump(t *testing.T) {
	dumptest.Use(t, dumptest.DumpFails)
	ctx := context.Background()
	ctrl := newController(t)
	ctrl.Tick(ctx, tick0)
	if bs := settled(t, ctrl, 1); bs[0].State != localapi.BackupFailed {
		t.Fatalf("backup %+v", bs[0])
	}
	ctrl.Tick(ctx, tick0.Add(14*time.Minute))
	settled(t, ctrl, 1)
	ctrl.Tick(ctx, tick0.Add(15*time.Minute))
	settled(t, ctrl, 2)
}

// A clock moved back must neither stop the schedule until it catches up nor
// fire on every tick.
func TestTickAfterTheClockMovedBack(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctx := context.Background()
	ctrl := newController(t)
	ctrl.Tick(ctx, tick0)
	settled(t, ctrl, 1)
	back := tick0.Add(-2 * time.Hour)
	ctrl.Tick(ctx, back)
	settled(t, ctrl, 2)
	ctrl.Tick(ctx, back.Add(time.Minute))
	settled(t, ctrl, 2)
}

func TestTickWithoutDatabaseDoesNothing(t *testing.T) {
	ctrl := newController(t)
	ctrl.Config.Controller.Database.Databases = nil
	ctrl.Tick(context.Background(), tick0)
	if bs, _ := ctrl.Backups(context.Background(), true); len(bs) != 0 {
		t.Errorf("backups %+v", bs)
	}
}
