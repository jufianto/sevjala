package coord

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/jufianto/sevjala/dump/dumptest"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/store"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) records(t *testing.T) []map[string]any {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(l.b.String()), "\n") {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("not JSON: %q", line)
		}
		out = append(out, r)
	}
	return out
}

func TestChangesLogWhoDidIt(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	var buf lockedBuffer
	ctrl.Log = logx.JSON(&buf)
	ctx := logx.WithBy(asUser, "tester")

	if _, err := ctrl.AddPolicy(ctx, localapi.PolicyChange{Name: "weekly", Keep: ptr(4)}); err != nil {
		t.Fatal(err)
	}
	if _, err := ctrl.SetPolicy(ctx, "weekly", localapi.PolicyChange{Keep: ptr(5)}); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.RemovePolicy(ctx, "weekly"); err != nil {
		t.Fatal(err)
	}
	jobs, err := ctrl.BackupNow(ctx, "shop", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, ctrl, jobs[0].BackupID, state(store.BackupCreated))
	ctrl.Tick(context.Background(), tick0)
	settled(t, ctrl, 2)

	policies, started := 0, 0
	for _, r := range buf.records(t) {
		switch r["event"] {
		case logx.EventPolicyChanged:
			policies++
			if r["by"] != "tester" {
				t.Errorf("policy event without by: %v", r)
			}
		case logx.EventBackupStarted:
			started++
			if mine := r["backup_id"] == jobs[0].BackupID; mine != (r["by"] == "tester") {
				t.Errorf("backup started (requested here: %v): %v", mine, r)
			}
		}
	}
	if policies != 3 || started != 2 {
		t.Errorf("%d policy events, %d backups started", policies, started)
	}
}
