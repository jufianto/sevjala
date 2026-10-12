package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kardianos/service"

	"github.com/jufianto/sevjala/config"
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
	if r := checkConfig(dir); r.OK {
		t.Errorf("missing config passed: %+v", r)
	}
	config.Write(config.Path(dir), config.Default(config.RoleNode))
	if r := checkConfig(dir); !r.OK || r.Detail != "role=node" {
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
