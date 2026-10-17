// Package manage carries manager links (blueprint AD-21): connections the
// controller opens to a manager node and then answers requests on. The node
// side is a Pool and a Relay; the controller side is a Keeper.
package manage

import (
	"context"
	"errors"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"
)

var errClosed = errors.New("manager links are shut down")

// Pool holds the idle links the controller opened to this node.
type Pool struct {
	mu     sync.Mutex
	idle   []*pooled
	signal chan struct{} // closed when a link arrives
	closed bool
}

// pooled is an idle link with a read pending on it: the controller sends
// nothing until asked, so the read only ends when the link closes or take
// interrupts it.
type pooled struct {
	c    net.Conn
	done chan error
}

func NewPool() *Pool { return &Pool{signal: make(chan struct{})} }

func (p *Pool) Put(c net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		c.Close()
		return
	}
	pl := &pooled{c: c, done: make(chan error, 1)}
	p.idle = append(p.idle, pl)
	close(p.signal)
	p.signal = make(chan struct{})
	go p.watch(pl)
}

func (p *Pool) watch(pl *pooled) {
	var b [1]byte
	_, err := pl.c.Read(b[:])
	if err == nil {
		err = errors.New("the controller sent data on an idle link")
	}
	p.mu.Lock()
	if i := slices.Index(p.idle, pl); i >= 0 {
		p.idle = slices.Delete(p.idle, i, i+1)
		pl.c.Close()
	}
	p.mu.Unlock()
	pl.done <- err
}

// Idle is how many links wait for a request.
func (p *Pool) Idle() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.idle)
}

// take waits for a live link until ctx ends.
func (p *Pool) take(ctx context.Context) (net.Conn, error) {
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, errClosed
		}
		if n := len(p.idle); n > 0 {
			pl := p.idle[n-1]
			p.idle = p.idle[:n-1]
			p.mu.Unlock()
			if c := claim(pl); c != nil {
				return c, nil
			}
			continue
		}
		sig := p.signal
		p.mu.Unlock()
		select {
		case <-sig:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// claim stops the idle read; the link is alive when that read timed out.
func claim(pl *pooled) net.Conn {
	_ = pl.c.SetReadDeadline(time.Now())
	err := <-pl.done
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		pl.c.Close()
		return nil
	}
	if pl.c.SetReadDeadline(time.Time{}) != nil {
		pl.c.Close()
		return nil
	}
	return pl.c
}

func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	for _, pl := range p.idle {
		pl.c.Close()
	}
	p.idle = nil
	close(p.signal)
}

// transport sends requests over pooled links, waiting at most wait for one.
func (p *Pool) transport(wait time.Duration) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(ctx, wait)
			defer cancel()
			c, err := p.take(ctx)
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, errNoLink
			}
			return c, err
		},
		// Below the controller's IdleClose, so this side drops a link first.
		IdleConnTimeout:     4 * time.Minute,
		MaxIdleConnsPerHost: Links,
	}
}
