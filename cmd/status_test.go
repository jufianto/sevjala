package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

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
