package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kardianos/service"

	"github.com/jufianto/sevjala/localapi"
)

type fakeService struct {
	st  service.Status
	err error
}

func (f fakeService) Status() (service.Status, error) { return f.st, f.err }
func (f fakeService) Platform() string                { return "test-platform" }

var answering = localapi.Status{Version: "0.1.0", Role: "node", PID: 4120, UptimeS: 133, DataDir: "/d"}

func TestBuildStatusView(t *testing.T) {
	down := errors.New("dial: no such file")
	tests := []struct {
		name       string
		svc        fakeService
		apiErr     error
		privileged bool
		service    string
		ok         bool
	}{
		{"running and answering", fakeService{st: service.StatusRunning}, nil, true, "running", true},
		{"running, agent down", fakeService{st: service.StatusRunning}, down, true, "running", false},
		{"stopped", fakeService{st: service.StatusStopped}, down, true, "stopped", false},
		{"not installed", fakeService{err: service.ErrNotInstalled}, down, false, "not installed", false},
		{"not installed but a dev agent answers", fakeService{err: service.ErrNotInstalled}, nil, false, "not installed", false},
		{"unprivileged sees stopped but agent answers", fakeService{st: service.StatusStopped}, nil, false, "unknown (run with sudo for the service state)", true},
		{"query error", fakeService{err: errors.New("boom")}, down, true, "unknown (boom)", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := buildStatusView(tt.svc, answering, tt.apiErr, "/d", tt.privileged)
			if v.Service != tt.service || v.OK() != tt.ok {
				t.Errorf("service=%q ok=%v, want %q %v", v.Service, v.OK(), tt.service, tt.ok)
			}
		})
	}
}

func TestRenderStatus(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, buildStatusView(fakeService{st: service.StatusRunning}, answering, nil, `C:\ProgramData\Sevjala`, true))
	want := "service   running      (test-platform)\n" +
		"agent     answering    0.1.0  role=node  up 2m13s  pid 4120\n" +
		"data dir  C:\\ProgramData\\Sevjala\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}

	b.Reset()
	renderStatus(&b, buildStatusView(fakeService{st: service.StatusStopped}, localapi.Status{}, errors.New("refused"), "/d", true))
	want = "service   stopped      (test-platform)\n" +
		"agent     not answering: the Sevjala service is stopped; start it with: sevjala service start\n" +
		"data dir  /d\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}

	b.Reset()
	renderStatus(&b, buildStatusView(fakeService{err: service.ErrNotInstalled}, localapi.Status{}, errors.New("refused"), "/d", true))
	if want := "service   not installed (test-platform)\n"; !strings.HasPrefix(b.String(), want) {
		t.Errorf("long label runs into the platform: %q", b.String())
	}
}

var oNow = time.Date(2026, 10, 17, 10, 27, 0, 0, time.Local)

func overviewOf(st localapi.Status, apiErr error, svcState service.Status) string {
	var b bytes.Buffer
	renderOverview(&b, buildStatusView(fakeService{st: svcState}, st, apiErr, "/d", true), "HOST", oNow)
	return b.String()
}

func TestRenderOverview(t *testing.T) {
	ctrl := localapi.Status{Version: "0.4.0", Role: "controller", Hostname: "SERVER-01", UptimeS: 2*86400 + 3*3600,
		Health: &localapi.Health{Level: localapi.HealthWarning, Headline: "PC-2 has not answered since 08:12 (2 h ago); 1 copy waits for it",
			Items: []localapi.HealthItem{
				{Level: localapi.HealthWarning, Text: "PC-2 has not answered since 08:12 (2 h ago); 1 copy waits for it"},
				{Level: localapi.HealthWarning, Text: "No schedule is on: backups run only with 'sevjala backup now'"}},
			Databases: []localapi.DBHealth{{Name: "shop", LastGood: "BK-2", LastGoodAt: oNow.Add(-7 * time.Minute), Size: 10 << 20, Copies: 1, Planned: 2},
				{Name: "legacy"}},
			NextBackupAt: oNow.Add(3 * time.Minute), NextPolicy: "frequent",
			Agents: []localapi.AgentHealth{{Hostname: "PC-1", State: "online", LastSeen: oNow},
				{Hostname: "PC-2", State: "offline", LastSeen: oNow.Add(-2*time.Hour - 15*time.Minute), Waiting: 1}},
			FreeBytes: 50 << 30}}
	node := localapi.Status{Version: "0.4.0", Role: "node", Hostname: "PC-1", UptimeS: 3600,
		Store: &localapi.StoreInfo{Backups: 34, FreeBytes: 120 << 30},
		Health: &localapi.Health{Level: localapi.HealthOK, Headline: "Holding 34 backups; the controller last checked in at 10:26 (just now)",
			Items: []localapi.HealthItem{}, FreeBytes: 120 << 30, ControllerContactAt: oNow.Add(-time.Minute)}}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"controller", overviewOf(ctrl, nil, service.StatusRunning), "Sevjala controller on SERVER-01: WARNING\n" +
			"  ! PC-2 has not answered since 08:12 (2 h ago); 1 copy waits for it\n" +
			"  ! No schedule is on: backups run only with 'sevjala backup now'\n\n" +
			"  service     running (test-platform), agent 0.4.0, up 2d 3h\n" +
			"  shop        last backup 10:20 (7 min ago), 10.0 MiB, on 1 of 2 backup PCs\n" +
			"  legacy      no backup on a backup PC yet\n" +
			"  next        10:30 (frequent)\n" +
			"  backup PCs  PC-1 online · PC-2 offline since 08:12\n" +
			"  disk        50.0 GiB free here\n"},
		{"node", overviewOf(node, nil, service.StatusRunning), "Sevjala backup PC on PC-1: OK\n" +
			"  Holding 34 backups; the controller last checked in at 10:26 (just now)\n\n" +
			"  service     running (test-platform), agent 0.4.0, up 1h 0m\n" +
			"  holds       34 backups, 120.0 GiB free\n"},
		{"not answering", overviewOf(localapi.Status{}, errors.New("refused"), service.StatusStopped), "Sevjala on HOST: PROBLEM\n" +
			"  ! the Sevjala service is stopped; start it with: sevjala service start\n\n" +
			"  service     stopped (test-platform)\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got\n%s\nwant\n%s", tt.got, tt.want)
			}
		})
	}
}

func TestOverviewOK(t *testing.T) {
	ok := func(level string) bool {
		var b bytes.Buffer
		st := localapi.Status{Role: "node", Health: &localapi.Health{Level: level}}
		return renderOverview(&b, buildStatusView(fakeService{st: service.StatusRunning}, st, nil, "/d", true), "H", oNow)
	}
	if !ok(localapi.HealthOK) || !ok(localapi.HealthWarning) || ok(localapi.HealthProblem) {
		t.Error("status exits 1 only for a problem or no answer")
	}
}
