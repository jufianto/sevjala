package manage

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
)

var discard = slog.New(slog.DiscardHandler)

// tcpPair is a connected pair of loopback TCP connections.
func tcpPair(t *testing.T) (a, b net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		got <- c
	}()
	a, err = net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return a, <-got
}

// fleet is a keeper on the controller side whose dial hands the other end of
// each link to the node's pool, as the LAN API does.
func fleet(t *testing.T, serve http.Handler, still func(string) bool) (*Keeper, *Pool) {
	t.Helper()
	pool := NewPool()
	k := NewKeeper(func(ctx context.Context, _ string) (net.Conn, error) {
		ctrlSide, nodeSide := tcpPair(t)
		pool.Put(nodeSide)
		return ctrlSide, nil
	}, serve, still, discard, "SERVER-01")
	t.Cleanup(func() { k.Close(); pool.Close() })
	return k, pool
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func managerState() RelayState {
	return RelayState{Role: "node", Paired: true, Known: true, Manager: true, Host: "SERVER-01", Self: "BACKUP-PC-1",
		LastContact: time.Now()}
}

// as runs h with the node's caller account set, as the local API does.
func as(account string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(logx.WithBy(r.Context(), account)))
	})
}

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
}

func errorOf(t *testing.T, body string) string {
	t.Helper()
	var e struct{ Error string }
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("not a JSON error: %q", body)
	}
	return e.Error
}

func TestRelayErrors(t *testing.T) {
	never := NewPool()
	defer never.Close()
	tests := []struct {
		name  string
		state RelayState
		code  int
		msg   string
	}{
		{"on the controller", RelayState{Role: "controller", Paired: true}, http.StatusConflict,
			"this PC is the controller; run the command without --controller"},
		{"not paired", RelayState{Role: "node"}, http.StatusConflict, "this PC is not paired with a controller"},
		{"no poll since start", RelayState{Role: "node", Paired: true}, http.StatusServiceUnavailable,
			"the controller has not contacted this PC since Sevjala started here (is it on and on the network?)"},
		{"not a manager", RelayState{Role: "node", Paired: true, Known: true, Host: "SERVER-01", Self: "BACKUP-PC-1"}, http.StatusForbidden,
			"this PC is not a manager of SERVER-01 (on SERVER-01, an administrator runs: sevjala agents manager BACKUP-PC-1 on)"},
		{"manager without a link", RelayState{Role: "node", Paired: true, Known: true, Manager: true, Host: "SERVER-01",
			LastContact: time.Now().Add(-2*time.Minute - 5*time.Second)}, http.StatusServiceUnavailable,
			"SERVER-01 has not connected to this PC for 2 min (is it on and on the network?)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := get(t, Relay(never, func() RelayState { return tt.state }, 50*time.Millisecond), "/controller/status")
			if code != tt.code || errorOf(t, body) != tt.msg {
				t.Errorf("%d %s", code, body)
			}
		})
	}
}

func TestRelayRoundTrip(t *testing.T) {
	serve := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		via, _ := localapi.ViaOf(r.Context())
		_ = json.NewEncoder(w).Encode(map[string]any{"path": r.URL.RequestURI(), "by": logx.By(r.Context()),
			"manager": via.Manager, "controller": via.Controller, "admin": localapi.CallerIsAdmin(r.Context())})
	})
	k, pool := fleet(t, serve, func(string) bool { return true })
	k.Sync([]Manager{{AgentID: "n1", Hostname: "BACKUP-PC-1"}})
	waitFor(t, "links", func() bool { return pool.Idle() == Links })

	code, body := get(t, as(`BACKUP-PC-1\anna`, Relay(pool, managerState, time.Second)), "/controller/backups?all=1")
	want := `{"admin":false,"by":"BACKUP-PC-1\\anna via BACKUP-PC-1","controller":"SERVER-01","manager":"BACKUP-PC-1","path":"/backups?all=1"}`
	if code != http.StatusOK || strings.TrimSpace(body) != want {
		t.Errorf("%d %s", code, body)
	}
}

func TestKeeperRefillsAndStops(t *testing.T) {
	k, pool := fleet(t, http.NotFoundHandler(), func(string) bool { return true })
	k.Sync([]Manager{{AgentID: "n1", Hostname: "BACKUP-PC-1"}})
	waitFor(t, "links", func() bool { return pool.Idle() == Links })

	c, err := pool.take(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	waitFor(t, "a new link", func() bool { return pool.Idle() == Links })

	k.Sync(nil)
	waitFor(t, "links closed", func() bool { return pool.Idle() == 0 })
}

func TestStillManagerIsCheckedPerRequest(t *testing.T) {
	var still atomic.Bool
	still.Store(true)
	k, pool := fleet(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") }),
		func(string) bool { return still.Load() })
	k.Sync([]Manager{{AgentID: "n1", Hostname: "BACKUP-PC-1"}})
	waitFor(t, "links", func() bool { return pool.Idle() == Links })
	still.Store(false)
	code, body := get(t, Relay(pool, managerState, time.Second), "/controller/status")
	if code != http.StatusForbidden || errorOf(t, body) != "BACKUP-PC-1 is no longer a manager of SERVER-01" {
		t.Errorf("%d %s", code, body)
	}
}

func TestPoolDropsDeadLinks(t *testing.T) {
	pool := NewPool()
	defer pool.Close()
	a, b := tcpPair(t)
	pool.Put(b)
	if pool.Idle() != 1 {
		t.Fatal("not pooled")
	}
	a.Close()
	waitFor(t, "the dead link dropped", func() bool { return pool.Idle() == 0 })
}

func TestStreamOutlivesIdleClose(t *testing.T) {
	old := IdleClose
	t.Cleanup(func() { IdleClose = old }) // after the keeper stops: cleanups run last-first
	IdleClose = 200 * time.Millisecond
	serve := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for range 6 {
			_, _ = io.WriteString(w, "chunk ")
			http.NewResponseController(w).Flush()
			time.Sleep(100 * time.Millisecond)
		}
	})
	k, pool := fleet(t, serve, func(string) bool { return true })
	k.Sync([]Manager{{AgentID: "n1", Hostname: "BACKUP-PC-1"}})
	waitFor(t, "a link", func() bool { return pool.Idle() > 0 })
	code, body := get(t, Relay(pool, managerState, time.Second), "/controller/held/BK-1")
	if code != http.StatusOK || body != strings.Repeat("chunk ", 6) {
		t.Errorf("%d %q", code, body)
	}
}

func TestLinkBreaksMidRequest(t *testing.T) {
	serve := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	k, pool := fleet(t, serve, func(string) bool { return true })
	k.Sync([]Manager{{AgentID: "n1", Hostname: "BACKUP-PC-1"}})
	waitFor(t, "a link", func() bool { return pool.Idle() > 0 })
	code, body := get(t, Relay(pool, managerState, time.Second), "/controller/backup/now")
	if code != http.StatusBadGateway || errorOf(t, body) != "the link to SERVER-01 broke mid-way; try again" {
		t.Errorf("%d %s", code, body)
	}
}
