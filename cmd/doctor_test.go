package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kardianos/service"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/lanapi"
	"github.com/jufianto/sevjala/localapi"
)

func TestCheckDataDir(t *testing.T) {
	dir := t.TempDir()
	if r := checkWritable("data dir", dir); !r.OK {
		t.Errorf("writable dir: %+v", r)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("probe file left behind: %v", entries)
	}
	if r := checkWritable("data dir", filepath.Join(dir, "missing")); r.OK {
		t.Errorf("missing dir passed: %+v", r)
	}
}

func TestCheckConfig(t *testing.T) {
	dir := t.TempDir()
	if r, _ := checkConfig(dir); r.OK {
		t.Errorf("missing config passed: %+v", r)
	}
	config.Write(config.Path(dir), config.Default(config.RoleNode))
	if r, _ := checkConfig(dir); !r.OK || r.Detail != "role=node" {
		t.Errorf("valid config: %+v", r)
	}
}

func TestServiceChecks(t *testing.T) {
	up := buildStatusView(fakeService{st: service.StatusRunning}, answering, nil, "/d", true)
	for _, r := range serviceChecks(up, "/d/sevjala.sock") {
		if !r.OK {
			t.Errorf("healthy view failed %s: %s", r.Name, r.Detail)
		}
	}
	down := buildStatusView(fakeService{err: service.ErrNotInstalled}, answering, errors.New("refused"), "/d", true)
	for _, r := range serviceChecks(down, "/d/sevjala.sock") {
		if r.OK {
			t.Errorf("broken view passed %s", r.Name)
		}
	}
}

func TestRenderChecks(t *testing.T) {
	var b bytes.Buffer
	ok := renderChecks(&b, []checkResult{
		{Name: "data dir", Detail: "/d", OK: true},
		{Name: "local api", Detail: "refused", OK: false},
	})
	want := "ok    data dir    /d\nFAIL  local api   refused\n"
	if ok || b.String() != want {
		t.Errorf("ok=%v\n%s\nwant\n%s", ok, b.String(), want)
	}
}

func TestCheckIdentityAndLAN(t *testing.T) {
	dir := t.TempDir()
	if r, id := checkIdentity(dir); r.OK || id != nil {
		t.Errorf("no identity passed: %+v", r)
	}
	id, _, err := identity.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, got := checkIdentity(dir)
	if !r.OK || got == nil || !strings.Contains(r.Detail, id.FP) {
		t.Errorf("identity: %+v", r)
	}

	srv, err := lanapi.Start(lanapi.Options{Addr: "127.0.0.1:0", DataDir: dir, Identity: id, Role: "node",
		Version: "test", Started: time.Now(), Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown(context.Background())
	cfg := config.Default(config.RoleNode)
	cfg.Listen = fmt.Sprintf("0.0.0.0:%d", srv.Port())
	if r := checkLAN(cfg, id); !r.OK {
		t.Errorf("own LAN API: %+v", r)
	}
	other, _, _ := identity.LoadOrCreate(t.TempDir())
	if r := checkLAN(cfg, other); r.OK || !strings.Contains(r.Detail, "not this agent") {
		t.Errorf("someone else's LAN API: %+v", r)
	}
	cfg.Listen = "127.0.0.1:1"
	if r := checkLAN(cfg, id); r.OK {
		t.Errorf("closed port passed: %+v", r)
	}
}

func TestStoreCheck(t *testing.T) {
	tests := []struct {
		name     string
		s        *localapi.StoreInfo
		ok, warn bool
	}{
		{"healthy", &localapi.StoreInfo{Path: "/s", FreeBytes: 10 << 30}, true, false},
		{"under the floor", &localapi.StoreInfo{Path: "/s", FreeBytes: 1 << 30}, false, false},
		{"problems", &localapi.StoreInfo{Path: "/s", FreeBytes: 10 << 30, Problems: []string{"x: manifest without its backup file"}}, false, true},
		{"missing", nil, false, false},
	}
	for _, tt := range tests {
		if r := storeCheck(tt.s, 2<<30); r.OK != tt.ok || r.Warn != tt.warn {
			t.Errorf("%s: %+v", tt.name, r)
		}
	}
}

func TestDatabaseChecks(t *testing.T) {
	d := localapi.Detection{Dumper: "/x/mysqldump", ServerVersion: "10.4.32-MariaDB", Flavour: "MariaDB",
		Databases: []localapi.DBDatabase{{Name: "shop", Consistency: "--single-transaction", Configured: true}}}
	rs := databaseChecks(d, nil)
	if len(rs) != 2 || !rs[0].OK || !rs[1].OK || !strings.Contains(rs[1].Detail, "shop single-transaction") {
		t.Errorf("%+v", rs)
	}
	d.Databases[0].Configured = false
	if rs := databaseChecks(d, nil); !rs[1].Warn {
		t.Errorf("nothing configured: %+v", rs)
	}
	if rs := databaseChecks(localapi.Detection{}, errors.New("Access denied")); len(rs) != 1 || rs[0].OK || rs[0].Warn {
		t.Errorf("error: %+v", rs)
	}
}

func TestRenderChecksWarnDoesNotFail(t *testing.T) {
	var b bytes.Buffer
	if !renderChecks(&b, []checkResult{{Name: "a", OK: true}, {Name: "b", Warn: true, Detail: "hmm"}}) {
		t.Error("a warning failed doctor")
	}
	if !strings.Contains(b.String(), "WARN  b") {
		t.Errorf("%q", b.String())
	}
}

func TestScheduleCheck(t *testing.T) {
	next := time.Date(2026, 10, 13, 10, 20, 0, 0, time.Local)
	ps := []localapi.Policy{
		{Name: "frequent", Enabled: true, NextRun: next},
		{Name: "hourly", Enabled: true, NextRun: next.Add(40 * time.Minute)},
		{Name: "sameminute", Enabled: true, NextRun: next},
		{Name: "manual", Enabled: true},
	}
	tests := []struct {
		name      string
		ps        []localapi.Policy
		err       error
		databases int
		ok, warn  bool
		detail    string
	}{
		{"next run", ps, nil, 1, true, false, "next run 2026-10-13 10:20 (frequent, sameminute)"},
		{"no database", ps, nil, 0, false, true, "idle"},
		{"nothing scheduled", ps[3:], nil, 1, false, true, "no enabled policy has a schedule"},
		{"agent error", nil, errors.New("boom"), 1, false, false, "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := scheduleCheck(tt.ps, tt.err, tt.databases)
			if r.OK != tt.ok || r.Warn != tt.warn || !strings.Contains(r.Detail, tt.detail) || r.Name != "schedule" {
				t.Errorf("%+v", r)
			}
		})
	}
}

func TestNodesCheck(t *testing.T) {
	up := localapi.Agent{Hostname: "PC-1", Role: "node", State: "online"}
	down := localapi.Agent{Hostname: "PC-2", Role: "node", State: "offline", Problem: "connection refused"}
	tests := []struct {
		name     string
		as       []localapi.Agent
		err      error
		ok, warn bool
		detail   string
	}{
		{"all online", []localapi.Agent{up}, nil, true, false, "1 online"},
		{"one offline", []localapi.Agent{up, down}, nil, false, true, "PC-2 offline (connection refused)"},
		{"none paired", nil, nil, false, false, "no paired node"},
		{"agent error", nil, errors.New("boom"), false, false, "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := nodesCheck(tt.as, tt.err)
			if r.OK != tt.ok || r.Warn != tt.warn || !strings.Contains(r.Detail, tt.detail) || r.Name != "nodes" {
				t.Errorf("%+v", r)
			}
		})
	}
}
