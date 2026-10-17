package manage

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/jufianto/sevjala/lanapi"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
)

// Links is how many links the controller keeps open to each manager.
const Links = 4

// IdleClose closes a link nobody used for this long; a request in progress
// does not count as idle.
var IdleClose = 5 * time.Minute

const maxBackoff = 15 * time.Second

// Manager is a backup PC the controller keeps links to.
type Manager struct{ AgentID, Hostname string }

// Keeper keeps Links links open to every manager it is told about and serves
// the local API on them as a non-admin caller from that manager.
type Keeper struct {
	dial  func(ctx context.Context, agentID string) (net.Conn, error)
	serve http.Handler
	still func(agentID string) bool
	log   *slog.Logger
	host  string

	mu     sync.Mutex
	loops  map[string]*loop
	closed bool
	wg     sync.WaitGroup
}

type loop struct {
	m      Manager
	cancel context.CancelFunc

	mu      sync.Mutex
	failing bool // the last dial failed and was logged
}

func NewKeeper(dial func(ctx context.Context, agentID string) (net.Conn, error), serve http.Handler,
	still func(agentID string) bool, log *slog.Logger, controllerHost string) *Keeper {
	return &Keeper{dial: dial, serve: serve, still: still, log: log, host: controllerHost, loops: map[string]*loop{}}
}

// Sync keeps links to exactly ms: new managers get links, the others lose theirs.
func (k *Keeper) Sync(ms []Manager) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return
	}
	want := map[string]Manager{}
	for _, m := range ms {
		want[m.AgentID] = m
	}
	for id, l := range k.loops {
		if m, ok := want[id]; !ok || m != l.m {
			l.cancel()
			delete(k.loops, id)
		}
	}
	for id, m := range want {
		if k.loops[id] != nil {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		l := &loop{m: m, cancel: cancel}
		k.loops[id] = l
		for range Links {
			k.wg.Go(func() { k.link(ctx, l) })
		}
	}
}

// Close drops every link and waits for them to end.
func (k *Keeper) Close() {
	k.mu.Lock()
	k.closed = true
	for id, l := range k.loops {
		l.cancel()
		delete(k.loops, id)
	}
	k.mu.Unlock()
	k.wg.Wait()
}

// link keeps one link to l.m open until ctx ends, redialling with backoff.
func (k *Keeper) link(ctx context.Context, l *loop) {
	backoff := time.Second
	for ctx.Err() == nil {
		conn, err := k.dial(ctx, l.m.AgentID)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			k.failed(ctx, l, err)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff = min(2*backoff, maxBackoff)
			continue
		}
		l.mu.Lock()
		l.failing = false
		l.mu.Unlock()
		backoff = time.Second
		k.serveLink(ctx, l.m, conn)
	}
}

func (k *Keeper) failed(ctx context.Context, l *loop, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failing {
		return
	}
	l.failing = true
	reason := err.Error()
	if errors.Is(err, lanapi.ErrOldNode) {
		reason = "update Sevjala on " + l.m.Hostname
	}
	k.log.WarnContext(ctx, "manager link failed", logx.Event(logx.EventManagerLinkFailed), "agent_id", l.m.AgentID,
		"hostname", l.m.Hostname, "reason", reason)
}

// serveLink answers the manager's requests on conn until it closes or ctx ends.
func (k *Keeper) serveLink(ctx context.Context, m Manager, conn net.Conn) {
	ln := &oneConn{c: conn, done: make(chan struct{})}
	srv := &http.Server{
		Handler:           k.guard(m),
		ReadHeaderTimeout: IdleClose,
		IdleTimeout:       IdleClose,
		ErrorLog:          slog.NewLogLogger(slog.DiscardHandler, slog.LevelDebug),
		ConnContext: func(c context.Context, _ net.Conn) context.Context {
			return localapi.WithVia(localapi.WithAdmin(c, false), localapi.Via{Manager: m.Hostname, Controller: k.host})
		},
		ConnState: func(_ net.Conn, s http.ConnState) {
			if s == http.StateClosed || s == http.StateHijacked {
				ln.Close()
			}
		},
	}
	stop := context.AfterFunc(ctx, func() { srv.Close() })
	defer stop()
	_ = srv.Serve(ln)
}

// guard re-checks the manager flag on every request and names the account
// that asked, as "<account> via <manager>".
func (k *Keeper) guard(m Manager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !k.still(m.AgentID) {
			w.Header().Set("Connection", "close")
			fail(w, http.StatusForbidden, "%s is no longer a manager of %s", m.Hostname, k.host)
			return
		}
		by := r.Header.Get(HeaderBy)
		if by == "" {
			by = "unknown"
		}
		k.serve.ServeHTTP(w, r.WithContext(logx.WithBy(r.Context(), by+" via "+m.Hostname)))
	})
}

// oneConn is a listener that yields one connection, then waits until it closes.
type oneConn struct {
	c    net.Conn
	once sync.Once
	took bool
	mu   sync.Mutex
	done chan struct{}
}

func (o *oneConn) Accept() (net.Conn, error) {
	o.mu.Lock()
	if !o.took {
		o.took = true
		o.mu.Unlock()
		return o.c, nil
	}
	o.mu.Unlock()
	<-o.done
	return nil, net.ErrClosed
}

func (o *oneConn) Close() error {
	o.once.Do(func() { close(o.done) })
	return nil
}

func (o *oneConn) Addr() net.Addr { return o.c.LocalAddr() }
