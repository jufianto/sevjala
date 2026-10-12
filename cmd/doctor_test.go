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
)

func TestCheckDataDir(t *testing.T) {
	dir := t.TempDir()
	if r := checkDataDir(dir); !r.OK {
		t.Errorf("writable dir: %+v", r)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("probe file left behind: %v", entries)
	}
	if r := checkDataDir(filepath.Join(dir, "missing")); r.OK {
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
