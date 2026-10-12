package cmd

import (
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
