package lanapi

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/pairing"
)

type linkSink chan net.Conn

func (s linkSink) Put(c net.Conn) { s <- c }

func startManagedNode(t *testing.T) (agent, linkSink) {
	t.Helper()
	id, dir := newID(t)
	codes := pairing.New(time.Now)
	sink := make(linkSink, 4)
	s, err := Start(Options{Addr: "127.0.0.1:0", DataDir: dir, Identity: id, Role: "node", Version: "test",
		Hostname: "host-node", Started: time.Now(), Codes: codes, Links: sink, Log: discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	return agent{dir: dir, id: id, codes: codes, srv: s}, sink
}

func wantManager(t *testing.T, s *Server, host string, manager, known bool) {
	t.Helper()
	if h, m, k := s.Manager(); h != host || m != manager || k != known {
		t.Errorf("Manager() = %q %v %v, want %q %v %v", h, m, k, host, manager, known)
	}
}

func TestPollHelloTeachesTheNode(t *testing.T) {
	n, _ := startManagedNode(t)
	c := controller(t)
	pair(t, c, n)
	ctx := context.Background()
	wantManager(t, n.srv, "", false, false)

	if _, err := c.PollStatus(ctx, n.srv.Addr(), n.id.FP, Hello{Manager: true, Host: "SERVER-01"}); err != nil {
		t.Fatal(err)
	}
	wantManager(t, n.srv, "SERVER-01", true, true)
	if _, err := c.Status(ctx, n.srv.Addr(), n.id.FP); err != nil { // a ping says nothing about managers
		t.Fatal(err)
	}
	wantManager(t, n.srv, "SERVER-01", true, true)
	if _, err := c.PollStatus(ctx, n.srv.Addr(), n.id.FP, Hello{Host: "SERVER-01"}); err != nil {
		t.Fatal(err)
	}
	wantManager(t, n.srv, "SERVER-01", false, true)
}

func TestManageUpgrade(t *testing.T) {
	n, sink := startManagedNode(t)
	c := controller(t)
	pair(t, c, n)
	ctx := context.Background()

	if _, err := c.Manage(ctx, n.srv.Addr(), n.id.FP); httpCode(err) != http.StatusForbidden {
		t.Fatalf("before the hello: %v", err)
	}
	if len(sink) != 0 {
		t.Fatal("a link was pooled for a node that is not a manager")
	}
	if _, err := c.PollStatus(ctx, n.srv.Addr(), n.id.FP, Hello{Manager: true, Host: "SERVER-01"}); err != nil {
		t.Fatal(err)
	}
	ctrlSide, err := c.Manage(ctx, n.srv.Addr(), n.id.FP)
	if err != nil {
		t.Fatal(err)
	}
	defer ctrlSide.Close()
	var nodeSide net.Conn
	select {
	case nodeSide = <-sink:
	case <-time.After(5 * time.Second):
		t.Fatal("no link pooled")
	}
	defer nodeSide.Close()

	// Roles swap: the node asks, the controller answers, on the same connection.
	go func() {
		req, err := http.ReadRequest(bufio.NewReader(ctrlSide))
		if err != nil || req.URL.Path != "/status" {
			t.Errorf("controller read %v %v", req, err)
			return
		}
		resp := &http.Response{StatusCode: 200, ProtoMajor: 1, ProtoMinor: 1, ContentLength: 5, Body: io.NopCloser(strings.NewReader("hello"))}
		_ = resp.Write(ctrlSide)
	}()
	req, _ := http.NewRequest(http.MethodGet, "http://controller/status", nil)
	if err := req.Write(nodeSide); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(nodeSide), req)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(resp.Body); string(b) != "hello" {
		t.Errorf("node read %q", b)
	}
}

func TestManageRefusesAStranger(t *testing.T) {
	n, sink := startManagedNode(t)
	pair(t, controller(t), n)
	if _, err := controller(t).Manage(context.Background(), n.srv.Addr(), n.id.FP); !errors.Is(err, ErrRejected) {
		t.Errorf("stranger: %v", err)
	}
	if len(sink) != 0 {
		t.Error("a stranger's link was pooled")
	}
}

func TestManageOnAnOldNode(t *testing.T) {
	old := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	}))
	defer old.Close()
	addr := strings.TrimPrefix(old.URL, "https://")
	if _, err := controller(t).Manage(context.Background(), addr, identity.Fingerprint(old.Certificate().Raw)); !errors.Is(err, ErrOldNode) {
		t.Errorf("old node: %v", err)
	}
}

func TestAPairingForgetsTheOldControllersHello(t *testing.T) {
	n, _ := startManagedNode(t)
	old := controller(t)
	pair(t, old, n)
	if _, err := old.PollStatus(context.Background(), n.srv.Addr(), n.id.FP, Hello{Manager: true, Host: "SERVER-01"}); err != nil {
		t.Fatal(err)
	}
	pair(t, controller(t), n)
	wantManager(t, n.srv, "", false, false)
}
