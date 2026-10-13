package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/kardianos/service"
)

func TestInstallRefusesWhenInstalled(t *testing.T) {
	err := checkNotInstalled(fakeService{st: service.StatusRunning})
	if err == nil || !strings.Contains(err.Error(), "already installed; run 'sevjala service uninstall' first") {
		t.Errorf("err = %v", err)
	}
	if err := checkNotInstalled(fakeService{err: service.ErrNotInstalled}); err != nil {
		t.Errorf("not installed: err = %v", err)
	}
}

func TestInstallRoleIsUsageError(t *testing.T) {
	for _, args := range [][]string{
		{"service", "install"},
		{"service", "install", "--role", "master"},
	} {
		var out, errOut strings.Builder
		if code := run(args, &out, &errOut); code != 2 {
			t.Errorf("%v: exit %d, want 2 (%s)", args, code, errOut.String())
		}
	}
}

type fakeControl struct {
	fakeService
	calls   []string
	failErr error
}

func (f *fakeControl) Start() error { f.calls = append(f.calls, "start"); return f.failErr }
func (f *fakeControl) Stop() error  { f.calls = append(f.calls, "stop"); return f.failErr }

func TestStartAndStopAreIdempotent(t *testing.T) {
	tests := []struct {
		name    string
		do      func(controllable) (string, error)
		ctl     fakeControl
		want    string
		wantErr string
		calls   int
	}{
		{"start a stopped service", startService, fakeControl{fakeService: fakeService{st: service.StatusStopped}}, "started", "", 1},
		{"start a running service", startService, fakeControl{fakeService: fakeService{st: service.StatusRunning}}, "already running", "", 0},
		{"stop a running service", stopService, fakeControl{fakeService: fakeService{st: service.StatusRunning}}, "stopped", "", 1},
		{"stop a stopped service", stopService, fakeControl{fakeService: fakeService{st: service.StatusStopped}}, "already stopped", "", 0},
		{"start when not installed", startService, fakeControl{fakeService: fakeService{err: service.ErrNotInstalled}}, "", "sevjala setup", 0},
		{"stop when not installed", stopService, fakeControl{fakeService: fakeService{err: service.ErrNotInstalled}}, "", "sevjala setup", 0},
		{"start fails", startService, fakeControl{fakeService: fakeService{st: service.StatusStopped}, failErr: errors.New("timed out")}, "", "could not start the Sevjala service: timed out", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.do(&tt.ctl)
			if got != tt.want || (err == nil) != (tt.wantErr == "") || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("got %q, %v; want %q, %q", got, err, tt.want, tt.wantErr)
			}
			if len(tt.ctl.calls) != tt.calls {
				t.Errorf("calls %q, want %d", tt.ctl.calls, tt.calls)
			}
		})
	}
}
