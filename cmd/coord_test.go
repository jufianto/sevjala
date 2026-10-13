package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kardianos/service"

	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/svc"
)

func TestRenderDiscovered(t *testing.T) {
	var b bytes.Buffer
	renderDiscovered(&b, []localapi.Discovered{
		{AgentID: "3f2a9c1e-1111-4111-8111-111111111111", Hostname: "BACKUP-PC-01", Addr: "192.168.1.21:17891", Role: "node", Version: "0.2.0", Paired: true},
		{AgentID: "9b000000-4444-4444-8444-444444444444", Hostname: "BACKUP-PC-02", Addr: "192.168.1.22:17891", Role: "node", Version: "0.2.0"},
	})
	want := "AGENT     HOSTNAME      ADDRESS             ROLE  VERSION  PAIRED\n" +
		"3f2a9c1e  BACKUP-PC-01  192.168.1.21:17891  node  0.2.0    yes\n" +
		"9b000000  BACKUP-PC-02  192.168.1.22:17891  node  0.2.0    no\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
	b.Reset()
	renderDiscovered(&b, nil)
	if !strings.Contains(b.String(), "sevjala agents add") || !strings.Contains(b.String(), "sevjala pair") {
		t.Errorf("empty: %q", b.String())
	}
}

func TestRenderCode(t *testing.T) {
	var b bytes.Buffer
	renderCode(&b, localapi.Code{Code: "123456", AgentID: "3f2a9c1e-1111", FP: "sha256:3f2a9b1c77de0a41ffff",
		ExpiresAt: time.Date(2026, 10, 12, 16, 42, 0, 0, time.Local)})
	want := "pairing code 123456, valid until 16:42\nfingerprint  3f2a9b1c77de0a41\n" +
		"on the controller run: sevjala pair 3f2a9c1e --code 123456 --fp 3f2a9b1c77de0a41\n" +
		"(if sevjala discover there does not list this PC, first run: sevjala agents add <this PC's IP>)\n"
	if b.String() != want {
		t.Errorf("got %q", b.String())
	}
}

func TestRenderAgents(t *testing.T) {
	var b bytes.Buffer
	seen := time.Date(2026, 10, 12, 16, 30, 0, 0, time.Local)
	renderAgents(&b, []localapi.Agent{
		{AgentID: "3f2a9c1e-1111", Hostname: "BACKUP-PC-01", Addr: "192.168.1.21:17891", State: "online", LastSeen: seen, FreeBytes: 3 << 30},
		{AgentID: "9b000000-4444", Hostname: "BACKUP-PC-02", Addr: "192.168.1.22:17891", State: "offline", Problem: "connection refused"},
	})
	want := "AGENT     HOSTNAME      ADDRESS             STATE    FREE     LAST SEEN         PROBLEM\n" +
		"3f2a9c1e  BACKUP-PC-01  192.168.1.21:17891  online   3.0 GiB  2026-10-12 16:30  \n" +
		"9b000000  BACKUP-PC-02  192.168.1.22:17891  offline  -        never             connection refused\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
	b.Reset()
	renderAgents(&b, nil)
	if !strings.Contains(b.String(), "no paired agents") || !strings.Contains(b.String(), "--fp") {
		t.Errorf("empty: %q", b.String())
	}
}

func TestRenderPing(t *testing.T) {
	var b bytes.Buffer
	renderPing(&b, localapi.Ping{Agent: localapi.Agent{Hostname: "BACKUP-PC-01", Addr: "192.168.1.21:17891", Role: "node", Version: "0.2.0"},
		RTTMs: 4, UptimeS: 7380, Health: "healthy"})
	want := "BACKUP-PC-01 (192.168.1.21:17891) answered in 4 ms: node 0.2.0, healthy, up 2h3m0s\n"
	if b.String() != want {
		t.Errorf("got %q", b.String())
	}
}

func TestCoordUsageErrors(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"pair", "abcd", "--code", "12"}, "6-digit"},
		{[]string{"pair", "abcd"}, "6-digit"},
		{[]string{"pair", "x", "--code", "123456"}, "--fp"},
		{[]string{"pair", "x", "--code", "123456", "--fp", "abc"}, "--fp"},
		{[]string{"pair", "--code", "123456"}, "accepts 1 arg"},
		{[]string{"ping"}, "accepts 1 arg"},
		{[]string{"agents", "add"}, "accepts 1 arg"},
		{[]string{"agents", "remove"}, "accepts 1 arg"},
	}
	for _, tt := range tests {
		var out, errOut bytes.Buffer
		if code := run(tt.args, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), tt.want) {
			t.Errorf("%v: exit %d, stderr %q", tt.args, code, errOut.String())
		}
	}
}

func TestCallAgentDeniedNamesTheFix(t *testing.T) {
	g := &globals{dataDirFlag: t.TempDir()}
	denied := &os.PathError{Op: "open", Path: `\\.\pipe\sevjala`, Err: fs.ErrPermission}
	_, err := callAgent(g, time.Second, func(context.Context, *localapi.Client) (struct{}, error) {
		return struct{}{}, fmt.Errorf("post: %w", denied)
	})
	if err == nil || !strings.Contains(err.Error(), svc.AdminHint) || strings.Contains(err.Error(), "is the service running") {
		t.Errorf("got %v, want the admin hint", err)
	}
}

func TestAgentProblem(t *testing.T) {
	pipe := func(err error) error {
		return fmt.Errorf("post: %w", &os.PathError{Op: "open", Path: `\\.\pipe\sevjala`, Err: err})
	}
	tests := []struct {
		name string
		err  error
		svc  serviceStatuser
		want string
	}{
		{"not installed", pipe(fs.ErrNotExist), fakeService{err: service.ErrNotInstalled}, "no Sevjala service installed"},
		{"stopped", pipe(fs.ErrNotExist), fakeService{st: service.StatusStopped}, "sevjala service start"},
		{"starting", pipe(fs.ErrNotExist), fakeService{st: service.StatusRunning}, "not answering yet"},
		{"state unknown", pipe(syscall.ECONNREFUSED), nil, "not running"},
		{"not admin", pipe(fs.ErrPermission), fakeService{st: service.StatusRunning}, svc.AdminHint},
		{"slow", fmt.Errorf("post: %w", context.DeadlineExceeded), nil, "did not answer in time"},
		{"restarted", fmt.Errorf("get: %w", io.ErrUnexpectedEOF), nil, "stopped answering"},
		{"agent's own message", &localapi.Error{Code: 409, Msg: "a backup of shop is already running"}, nil, "a backup of shop is already running"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentProblem(tt.err, tt.svc).Error()
			if !strings.Contains(got, tt.want) {
				t.Errorf("got %q, want it to contain %q", got, tt.want)
			}
			for _, raw := range []string{`\\.\pipe`, "post:", "http://", "file specified"} {
				if strings.Contains(got, raw) && tt.name != "agent's own message" {
					t.Errorf("%q leaks %q", got, raw)
				}
			}
		})
	}
}
