package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/localapi"
)

func script(states ...localapi.Backup) func(context.Context, string) (localapi.Backup, error) {
	i := 0
	return func(context.Context, string) (localapi.Backup, error) {
		b := states[min(i, len(states)-1)]
		i++
		return b, nil
	}
}

func copies(states ...string) []localapi.BackupCopy {
	var out []localapi.BackupCopy
	for i, s := range states {
		out = append(out, localapi.BackupCopy{Hostname: "PC-" + string(rune('1'+i)), State: s})
	}
	return out
}

func TestWatchBackups(t *testing.T) {
	sha := strings.Repeat("a", 64)
	id := "BK-20261013-101500-A8F2"
	base := localapi.Backup{BackupID: id, Database: "shop", Size: 3 << 20, SHA256: sha}
	with := func(state string, cs []localapi.BackupCopy) localapi.Backup {
		b := base
		b.State, b.Copies = state, cs
		return b
	}
	tests := []struct {
		name   string
		steps  []localapi.Backup
		ok     bool
		output []string
	}{
		{"all verified", []localapi.Backup{
			{BackupID: id, Database: "shop", State: localapi.BackupDumping, Bytes: 1 << 20},
			with("created", copies("transferring", "pending")),
			with("distributed", copies("verified", "verified")),
		}, true, []string{"dumping… 1.0 MiB", "created 3.0 MiB sha256:aaaaaaaaaaaa", "PC-1 verified · PC-2 verified"}},
		{"dump failed", []localapi.Backup{{BackupID: id, Database: "shop", State: localapi.BackupFailed, Error: "Unknown database"}},
			false, []string{"dump failed: Unknown database"}},
		{"one copy failed", []localapi.Backup{
			with("partial", []localapi.BackupCopy{{Hostname: "PC-1", State: "verified"}, {Hostname: "PC-2", State: "failed", LastError: "422"}}),
		}, false, []string{"PC-2 failed (422)"}},
		{"no nodes", []localapi.Backup{with("created", nil)}, true, []string{"no paired nodes"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b bytes.Buffer
			ok, err := watchBackups(context.Background(), script(tt.steps...), []localapi.BackupJob{{BackupID: id}}, &b, time.Millisecond)
			if err != nil || ok != tt.ok {
				t.Fatalf("ok = %v, err = %v\n%s", ok, err, b.String())
			}
			for _, want := range tt.output {
				if !strings.Contains(b.String(), want) {
					t.Errorf("missing %q in\n%s", want, b.String())
				}
			}
		})
	}
}

func TestWatchStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var b bytes.Buffer
	ok, err := watchBackups(ctx, script(localapi.Backup{State: localapi.BackupDumping}), []localapi.BackupJob{{BackupID: "x"}}, &b, time.Hour)
	if ok || err != nil {
		t.Errorf("ok = %v, err = %v", ok, err)
	}
}

func TestRenderBackupsAndMatrix(t *testing.T) {
	bs := []localapi.Backup{{BackupID: "BK-20261013-101500-A8F2", Database: "shop", Size: 2048, Tags: []string{"manual"},
		CreatedAt: time.Date(2026, 10, 13, 10, 15, 0, 0, time.Local), State: "partial",
		Copies: []localapi.BackupCopy{{Hostname: "PC-1", State: "verified"}, {Hostname: "PC-2", State: "offline"},
			{Hostname: "PC-3", State: "corrupt"}, {Hostname: "PC-4", State: "deleted"}}}}
	var b bytes.Buffer
	renderBackups(&b, bs, nil)
	if out := b.String(); !strings.Contains(out, "1/3 verified") || !strings.Contains(out, "2026-10-13 10:15") || !strings.Contains(out, "manual") {
		t.Errorf("list:\n%s", out)
	}
	b.Reset()
	renderMatrix(&b, bs, "linux")
	if out := b.String(); !strings.Contains(out, "PC-1") || !strings.Contains(out, "✓     ○     !     -") {
		t.Errorf("matrix:\n%s", out)
	}
	b.Reset()
	renderMatrix(&b, bs, "windows")
	if out := b.String(); strings.ContainsAny(out, "✓○") || !strings.Contains(out, " v ") {
		t.Errorf("windows matrix:\n%s", out)
	}
	b.Reset()
	renderBackups(&b, nil, nil)
	if !strings.Contains(b.String(), "sevjala backup now") {
		t.Errorf("empty list: %q", b.String())
	}
}

func TestRenderBackupsOrdersTagsAndExplainsStates(t *testing.T) {
	bs := []localapi.Backup{
		{BackupID: "BK-2", Tags: []string{"daily", "frequent", "hourly", "monthly", "yearend"}, State: "distributed"},
		{BackupID: "BK-1", Tags: []string{"manual"}, State: "partial"},
	}
	var b bytes.Buffer
	renderBackups(&b, bs, []string{"frequent", "hourly", "daily", "monthly", "manual"})
	out := b.String()
	if !strings.Contains(out, "frequent,hourly,daily,monthly,yearend") {
		t.Errorf("tags not in policy order:\n%s", out)
	}
	if !strings.Contains(out, "distributed = on every backup PC it should be on") || !strings.Contains(out, "partial = ") {
		t.Errorf("no legend for the states shown:\n%s", out)
	}
	if strings.Contains(out, "expired = ") {
		t.Errorf("legend explains a state not shown:\n%s", out)
	}
}
