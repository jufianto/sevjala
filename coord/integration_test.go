package coord

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/discovery"
	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/lanapi"
	"github.com/jufianto/sevjala/pairing"
	"github.com/jufianto/sevjala/store"
)

var discard = slog.New(slog.DiscardHandler)

type testAgent struct {
	id    *identity.Identity
	codes *pairing.Codes
	lan   *lanapi.Server
	disco *discovery.Responder
}

func startAgent(t *testing.T, role, hostname string) testAgent {
	t.Helper()
	dir := t.TempDir()
	id, _, err := identity.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	var codes *pairing.Codes
	if role == "node" {
		codes = pairing.New(time.Now)
	}
	lan, err := lanapi.Start(lanapi.Options{Addr: "127.0.0.1:0", DataDir: dir, Identity: id, Role: role,
		Version: "test", Hostname: hostname, Started: time.Now(), Codes: codes, Log: discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lan.Shutdown(context.Background()) })
	info := discovery.Reply{AgentID: id.AgentID, Hostname: hostname, Port: lan.Port(), Role: role,
		Version: "test", OS: runtime.GOOS, FP: id.FP}
	disco, err := discovery.Listen("127.0.0.1:0", func() discovery.Reply { return info }, discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { disco.Close() })
	return testAgent{id: id, codes: codes, lan: lan, disco: disco}
}

func newController(t *testing.T, targets ...*discovery.Responder) *Controller {
	t.Helper()
	dir := t.TempDir()
	id, _, err := identity.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Controller{Self: id, Store: st, LAN: lanapi.NewClient(id), Wait: 300 * time.Millisecond, Log: discard,
		Targets: func() ([]*net.UDPAddr, error) {
			var out []*net.UDPAddr
			for _, r := range targets {
				out = append(out, r.Addr())
			}
			return out, nil
		}}
}

func TestDiscoverPairPing(t *testing.T) {
	ctx := context.Background()
	node := startAgent(t, "node", "BACKUP-PC-01")
	ctrl := newController(t, node.disco)

	found, err := ctrl.Discover(ctx)
	if err != nil || len(found) != 1 || found[0].AgentID != node.id.AgentID || found[0].Paired || found[0].Role != "node" {
		t.Fatalf("discover: %+v %v", found, err)
	}

	code, _, _ := node.codes.Issue()
	a, err := ctrl.Pair(ctx, node.id.AgentID[:8], code)
	if err != nil || a.AgentID != node.id.AgentID || a.FP != node.id.FP || a.State != "online" {
		t.Fatalf("pair: %+v %v", a, err)
	}

	p, err := ctrl.Ping(ctx, "backup-pc-01")
	if err != nil || p.Agent.AgentID != node.id.AgentID || p.Health != "healthy" || p.Agent.LastSeen.IsZero() {
		t.Fatalf("ping: %+v %v", p, err)
	}

	agents, err := ctrl.Agents(ctx)
	if err != nil || len(agents) != 1 || agents[0].Hostname != "BACKUP-PC-01" {
		t.Fatalf("agents: %+v %v", agents, err)
	}

	again, _ := ctrl.Discover(ctx)
	if len(again) != 1 || !again[0].Paired {
		t.Errorf("rediscover: %+v", again)
	}

	stranger := newController(t)
	if _, err := stranger.LAN.Status(ctx, node.lan.Addr(), node.id.FP); !errors.Is(err, lanapi.ErrRejected) {
		t.Errorf("unpaired controller: %v, want a TLS rejection", err)
	}
}

func TestPairWrongCode(t *testing.T) {
	ctx := context.Background()
	node := startAgent(t, "node", "PC")
	ctrl := newController(t, node.disco)
	ctrl.Discover(ctx)
	code, _, _ := node.codes.Issue()
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if _, err := ctrl.Pair(ctx, "PC", wrong); err == nil || !strings.Contains(err.Error(), "wrong pairing code") {
		t.Errorf("err = %v", err)
	}
	if as, _ := ctrl.Agents(ctx); len(as) != 0 {
		t.Errorf("stored after a failed pair: %+v", as)
	}
}

func TestPairNeedsDiscovery(t *testing.T) {
	_, err := newController(t).Pair(context.Background(), "abcd", "123456")
	if err == nil || !strings.Contains(err.Error(), "sevjala discover") {
		t.Errorf("err = %v", err)
	}
}

func TestPairRejectsBadCode(t *testing.T) {
	_, err := newController(t).Pair(context.Background(), "abcd", "12")
	if err == nil || !strings.Contains(err.Error(), "6 digits") {
		t.Errorf("err = %v", err)
	}
}

func TestPairRefusesController(t *testing.T) {
	ctx := context.Background()
	other := startAgent(t, "controller", "SERVER-02")
	ctrl := newController(t, other.disco)
	ctrl.Discover(ctx)
	if _, err := ctrl.Pair(ctx, "SERVER-02", "123456"); err == nil || !strings.Contains(err.Error(), "only nodes can be paired") {
		t.Errorf("err = %v", err)
	}
}

func TestAddAgentThenPair(t *testing.T) {
	ctx := context.Background()
	node := startAgent(t, "node", "PC-MANUAL")
	ctrl := newController(t)
	d, err := ctrl.AddAgent(ctx, node.lan.Addr())
	if err != nil || d.AgentID != node.id.AgentID || d.Addr != node.lan.Addr() {
		t.Fatalf("add: %+v %v", d, err)
	}
	code, _, _ := node.codes.Issue()
	if _, err := ctrl.Pair(ctx, "PC-MANUAL", code); err != nil {
		t.Fatalf("pair: %v", err)
	}
}

func TestControllerSkipsItself(t *testing.T) {
	ctx := context.Background()
	ctrl := newController(t)
	info := discovery.Reply{AgentID: ctrl.Self.AgentID, Hostname: "SELF", Role: "controller", FP: ctrl.Self.FP}
	self, err := discovery.Listen("127.0.0.1:0", func() discovery.Reply { return info }, discard)
	if err != nil {
		t.Fatal(err)
	}
	defer self.Close()
	ctrl.Targets = func() ([]*net.UDPAddr, error) { return []*net.UDPAddr{self.Addr()}, nil }
	if found, err := ctrl.Discover(ctx); err != nil || len(found) != 0 {
		t.Errorf("found %+v, %v", found, err)
	}
}

func TestDiscoverRefreshesPairedAddress(t *testing.T) {
	ctx := context.Background()
	node := startAgent(t, "node", "PC-DHCP")
	ctrl := newController(t, node.disco)
	ctrl.Discover(ctx)
	code, _, _ := node.codes.Issue()
	if _, err := ctrl.Pair(ctx, "PC-DHCP", code); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.Store.UpdateAddress(ctx, node.id.AgentID, "PC-DHCP", "10.255.255.1", 1); err != nil {
		t.Fatal(err)
	}
	ctrl.Discover(ctx)
	if _, err := ctrl.Ping(ctx, "PC-DHCP"); err != nil {
		t.Errorf("ping after rediscovery: %v", err)
	}
}
