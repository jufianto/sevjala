package discovery

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

var discard = slog.New(slog.DiscardHandler)

func respond(t *testing.T, r Reply) *Responder {
	t.Helper()
	res, err := Listen("127.0.0.1:0", func() Reply { return r }, discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Close() })
	return res
}

func TestProbeFindsResponder(t *testing.T) {
	want := Reply{AgentID: "a1", Hostname: "PC-1", Port: 17891, Role: "node", Version: "0.2.0", OS: "windows", FP: "sha256:ab"}
	res := respond(t, want)
	got, err := Probe(context.Background(), []*net.UDPAddr{res.Addr()}, 300*time.Millisecond)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	g := got[0]
	if g.AgentID != "a1" || g.Hostname != "PC-1" || g.Port != 17891 || g.FP != "sha256:ab" || g.IP != "127.0.0.1" || g.V != 1 {
		t.Errorf("reply %+v", g)
	}
}

func TestProbeDedupesAndSorts(t *testing.T) {
	a := respond(t, Reply{AgentID: "a", Hostname: "zeta"})
	b := respond(t, Reply{AgentID: "b", Hostname: "alpha"})
	got, err := Probe(context.Background(), []*net.UDPAddr{a.Addr(), a.Addr(), b.Addr()}, 300*time.Millisecond)
	if err != nil || len(got) != 2 || got[0].Hostname != "alpha" || got[1].Hostname != "zeta" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestWrongNonceDropped(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go func() {
		buf := make([]byte, 512)
		_, from, err := conn.ReadFromUDP(buf)
		if err == nil {
			conn.WriteToUDP([]byte(`{"v":1,"nonce":"0000000000000000","agent_id":"x"}`), from)
		}
	}()
	got, err := Probe(context.Background(), []*net.UDPAddr{conn.LocalAddr().(*net.UDPAddr)}, 300*time.Millisecond)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestResponderIgnoresJunk(t *testing.T) {
	res := respond(t, Reply{AgentID: "a"})
	c, err := net.DialUDP("udp4", nil, res.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, junk := range []string{"hello", Prefix + "nothex!!nothex!!", Prefix + "abc", Prefix + strings.Repeat("a", 300)} {
		c.Write([]byte(junk))
	}
	c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if n, err := c.Read(make([]byte, 512)); err == nil {
		t.Fatalf("responder answered junk with %d bytes", n)
	}
}

func TestProbeWithoutTargets(t *testing.T) {
	_, err := Probe(context.Background(), nil, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no broadcast-capable IPv4 interface") {
		t.Fatalf("err = %v", err)
	}
}

func TestBroadcastAddr(t *testing.T) {
	tests := []struct{ cidr, want string }{
		{"192.168.1.21/24", "192.168.1.255"},
		{"10.0.5.9/16", "10.0.255.255"},
		{"172.16.3.4/20", "172.16.15.255"},
		{"10.1.1.1/32", ""},
		{"fe80::1/64", ""},
	}
	for _, tt := range tests {
		ip, n, _ := net.ParseCIDR(tt.cidr)
		n.IP = ip
		got := broadcastAddr(n)
		if (got == nil && tt.want != "") || (got != nil && got.String() != tt.want) {
			t.Errorf("%s: got %v, want %q", tt.cidr, got, tt.want)
		}
	}
}
