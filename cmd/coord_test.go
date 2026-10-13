package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/localapi"
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
	if !strings.Contains(b.String(), "sevjala agents add") {
		t.Errorf("empty: %q", b.String())
	}
}

func TestRenderCode(t *testing.T) {
	var b bytes.Buffer
	renderCode(&b, localapi.Code{Code: "123456", AgentID: "3f2a9c1e-1111", FP: "sha256:3f2a9b1c77de0a41ffff",
		ExpiresAt: time.Date(2026, 10, 12, 16, 42, 0, 0, time.Local)})
	want := "pairing code 123456, valid until 16:42\nfingerprint  3f2a9b1c77de0a41\n" +
		"on the controller run: sevjala pair 3f2a9c1e --code 123456 --fp 3f2a9b1c77de0a41\n"
	if b.String() != want {
		t.Errorf("got %q", b.String())
	}
}

func TestRenderAgents(t *testing.T) {
	var b bytes.Buffer
	seen := time.Date(2026, 10, 12, 16, 30, 0, 0, time.Local)
	renderAgents(&b, []localapi.Agent{
		{AgentID: "3f2a9c1e-1111", Hostname: "BACKUP-PC-01", Addr: "192.168.1.21:17891", State: "online", LastSeen: seen},
		{AgentID: "9b000000-4444", Hostname: "BACKUP-PC-02", Addr: "192.168.1.22:17891", State: "online"},
	})
	want := "AGENT     HOSTNAME      ADDRESS             STATE   LAST SEEN\n" +
		"3f2a9c1e  BACKUP-PC-01  192.168.1.21:17891  online  2026-10-12 16:30\n" +
		"9b000000  BACKUP-PC-02  192.168.1.22:17891  online  never\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
	b.Reset()
	renderAgents(&b, nil)
	if !strings.Contains(b.String(), "no paired agents") {
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
	}
	for _, tt := range tests {
		var out, errOut bytes.Buffer
		if code := run(tt.args, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), tt.want) {
			t.Errorf("%v: exit %d, stderr %q", tt.args, code, errOut.String())
		}
	}
}
