package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/localapi"
)

func TestControllerFlagOnlyWithControllerCommands(t *testing.T) {
	for _, args := range [][]string{
		{"--controller", "service", "status"},
		{"--controller", "agents", "manager", "PC-1", "on"},
		{"code", "--controller"},
		{"-c", "service", "status"},
		{"code", "-c"},
	} {
		var out, errOut bytes.Buffer
		code := run(append(args, "--data-dir", t.TempDir()), &out, &errOut)
		if code != 2 || !strings.Contains(errOut.String(),
			"--controller works with: status, discover, pair, agents, ping, db, backup, backups, restore, policy, retention, ui") {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut.String())
		}
	}
}

func TestAgentsManagerTakesOnOrOff(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"agents", "manager", "PC-1", "maybe", "--data-dir", t.TempDir()}, &out, &errOut); code != 2 ||
		!strings.Contains(errOut.String(), `"on" or "off"`) {
		t.Errorf("exit %d, stderr %q", code, errOut.String())
	}
}

func TestControllerAdminRequiredIsNotElevated(t *testing.T) {
	g := &globals{dataDirFlag: t.TempDir(), controller: true}
	via := localapi.WithVia(context.Background(), localapi.Via{Manager: "BACKUP-PC-1", Controller: "SERVER-01"})
	_, err := callAgent(g, time.Second, func(context.Context, *localapi.Client) (struct{}, error) {
		return struct{}{}, localapi.AdminRequired(via, "restoring from a file")
	})
	var na *needsAdmin
	if errors.As(err, &na) || err == nil || !strings.Contains(err.Error(), "needs administrator rights on SERVER-01 itself") {
		t.Errorf("got %#v", err)
	}
}

func TestRenderOverviewViaManager(t *testing.T) {
	st := localapi.Status{Version: "0.4.0", Role: "controller", Hostname: "SERVER-01", UptimeS: 3600,
		Health: &localapi.Health{Level: localapi.HealthOK, Headline: "Protected", FreeBytes: 8 << 30}}
	var b bytes.Buffer
	renderOverview(&b, viaManagerView(st, nil, "BACKUP-PC-1"), "BACKUP-PC-1", oNow)
	got := b.String()
	if !strings.HasPrefix(got, "Sevjala controller on SERVER-01 (via manager link): OK\n") ||
		!strings.Contains(got, "service     reached through this PC's manager link (BACKUP-PC-1), agent 0.4.0, up 1h 0m") ||
		!strings.Contains(got, "disk        8.0 GiB free on SERVER-01") {
		t.Errorf("got\n%s", got)
	}

	b.Reset()
	ok := renderOverview(&b, viaManagerView(localapi.Status{}, localapi.Errorf(403, "this PC is not a manager of SERVER-01"), "BACKUP-PC-1"), "BACKUP-PC-1", oNow)
	if ok || b.String() != "Sevjala controller (via manager link): PROBLEM\n  ! this PC is not a manager of SERVER-01\n" {
		t.Errorf("error view %v\n%s", ok, b.String())
	}
}

func TestAgentsNameTakesANameOrClear(t *testing.T) {
	for _, args := range [][]string{
		{"agents", "name", "PC-1"},
		{"agents", "name", "PC-1", "Office PC", "--clear"},
		{"agents", "name", "PC-1", "  "},
	} {
		var out, errOut bytes.Buffer
		if code := run(append(args, "--data-dir", t.TempDir()), &out, &errOut); code != 2 ||
			!strings.Contains(errOut.String(), "give the new name, or --clear") {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut.String())
		}
	}
}
