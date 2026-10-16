package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/localapi"
)

func restoreScript(states ...localapi.Restore) func(context.Context) (localapi.Restore, error) {
	i := 0
	return func(context.Context) (localapi.Restore, error) {
		r := states[min(i, len(states)-1)]
		i++
		return r, nil
	}
}

func TestWatchRestore(t *testing.T) {
	start := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	run := func(step string, bytes, size int64) localapi.Restore {
		return localapi.Restore{BackupID: "BK-20261016-095500-A8F2", Database: "shop", Target: "shop_copy",
			State: localapi.RestoreRunning, Step: step, Bytes: bytes, Size: size, StartedAt: start}
	}
	done := run("loading into shop_copy", 10<<20, 10<<20)
	done.State, done.Tables, done.Source, done.FinishedAt = localapi.RestoreDone, 42, "the controller's own copy", start.Add(130*time.Second)
	failed := run("loading into shop_copy", 1<<20, 10<<20)
	failed.State, failed.Error = localapi.RestoreFailed, "mysql failed: ERROR 1062; the half-made database shop_copy was removed"
	replaced := done
	replaced.Replace, replaced.Target, replaced.PreRestoreID = true, "shop", "BK-20261016-100000-B001"

	for _, tt := range []struct {
		name  string
		steps []localapi.Restore
		ok    bool
		want  []string
	}{
		{"into a new database", []localapi.Restore{
			run("checking the controller's own copy", 0, 10<<20),
			run("loading into shop_copy", 5<<20, 10<<20),
			run("loading into shop_copy", 6<<20, 10<<20),
			done,
		}, true, []string{"checking the controller's own copy…", "loading into shop_copy… 50% (5.0 MiB of 10.0 MiB)",
			"Restored BK-20261016-095500-A8F2 into shop_copy: 42 tables, in 2m10s, from the controller's own copy.",
			"shop was not touched"}},
		{"failed", []localapi.Restore{failed}, false, []string{"Restore failed: mysql failed: ERROR 1062"}},
		{"replaced", []localapi.Restore{replaced}, true, []string{"Replaced shop with BK-20261016-095500-A8F2: 42 tables",
			"sevjala restore BK-20261016-100000-B001 --replace-production"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var b bytes.Buffer
			ok, err := watchRestore(context.Background(), restoreScript(tt.steps...), &b, time.Millisecond)
			if err != nil || ok != tt.ok {
				t.Errorf("ok = %v, err = %v", ok, err)
			}
			for _, w := range tt.want {
				if !strings.Contains(b.String(), w) {
					t.Errorf("no %q in\n%s", w, b.String())
				}
			}
			if strings.Contains(b.String(), "60%") {
				t.Errorf("percentage printed more often than every 5 s:\n%s", b.String())
			}
		})
	}
}

func TestConfirmReplace(t *testing.T) {
	p := localapi.Restore{BackupID: "BK-20261016-095500-A8F2", Database: "shop", Target: "shop", Replace: true, Size: 11 << 20,
		BackupCreatedAt: time.Date(2026, 10, 16, 9, 55, 0, 0, time.Local)}
	for typed, want := range map[string]bool{"shop\n": true, "shop\r\n": true, " shop \n": true, "Shop\n": false, "\n": false, "": false} {
		var out bytes.Buffer
		got := confirmReplace(strings.NewReader(typed), &out, p)
		if (got == p.Target) != want {
			t.Errorf("typed %q: got %q", typed, got)
		}
		for _, w := range []string{"replaces the live database shop", "BK-20261016-095500-A8F2", "2026-10-16 09:55", "11.0 MiB",
			"unavailable", "Type the database name (shop)"} {
			if !strings.Contains(out.String(), w) {
				t.Errorf("no %q in\n%s", w, out.String())
			}
		}
	}
}

func TestRestoreUsageErrors(t *testing.T) {
	dir := t.TempDir()
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"restore"}, "missing <backup|latest>"},
		{[]string{"restore", "latest", "--file", "x.sql.gz"}, "not both"},
		{[]string{"restore", "latest", "--into", "a", "--replace-production"}, "into"},
		{[]string{"restore", "latest", "--status"}, "--status"},
		{[]string{"restore", "--status", "--db", "shop"}, "--status"},
	} {
		var out, errOut bytes.Buffer
		if code := run(append(tt.args, "--data-dir", dir), &out, &errOut); code != 2 || !strings.Contains(errOut.String(), tt.want) {
			t.Errorf("%v: exit %d, stderr %s", tt.args, code, errOut.String())
		}
	}
}

// The backup the operator confirmed is the one restored, even when "latest"
// moves on while the prompt waits.
func TestConfirmedRequestPinsTheBackup(t *testing.T) {
	plan := localapi.Restore{BackupID: "BK-20261016-020000-A8F2", Database: "shop", Target: "shop", Replace: true}
	req := localapi.RestoreRequest{Backup: "latest", DB: "shop", Replace: true}
	got := confirmedRequest(req, plan, "shop")
	if got.Backup != plan.BackupID || got.DB != "" || got.Confirm != "shop" || !got.Replace || got.DryRun {
		t.Errorf("request %+v", got)
	}
	file := localapi.RestoreRequest{File: "/x/a.sql.gz", Replace: true}
	if got := confirmedRequest(file, plan, "shop"); got.Backup != "" || got.File != file.File {
		t.Errorf("file request %+v", got)
	}
}
