// Package transfer pushes staged backups to nodes: one worker per node and
// whole-file retries with backoff (blueprint: Backup engine → Transfer).
package transfer

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jufianto/sevjala/store"
)

type Job struct {
	BackupID string
	AgentID  string
}

// DefaultDelays are the waits before retries 1-3; after them a copy is failed
// (or offline) until M3's /status cycle picks it up again.
var DefaultDelays = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute}

const queueSize = 1024

type permanentError struct{ error }

func (e permanentError) Unwrap() error { return e.error }

// Permanent marks an error a retry cannot fix.
func Permanent(err error) error { return permanentError{err} }

type unreachableError struct{ error }

func (e unreachableError) Unwrap() error { return e.error }

// Unreachable marks an error where the node could not be reached at all.
func Unreachable(err error) error { return unreachableError{err} }

type Pool struct {
	push   func(context.Context, Job) error
	settle func(j Job, state string, attempts int, err error)
	delays []time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	queues map[string]chan Job
}

func NewPool(push func(context.Context, Job) error, settle func(Job, string, int, error), delays []time.Duration) *Pool {
	ctx, cancel := context.WithCancel(context.Background())
	return &Pool{push: push, settle: settle, delays: delays, ctx: ctx, cancel: cancel, queues: map[string]chan Job{}}
}

func (p *Pool) Enqueue(j Job) {
	p.mu.Lock()
	if p.ctx.Err() != nil {
		p.mu.Unlock()
		return
	}
	q, ok := p.queues[j.AgentID]
	if !ok {
		q = make(chan Job, queueSize)
		p.queues[j.AgentID] = q
		p.wg.Add(1)
		go p.work(q)
	}
	p.mu.Unlock()
	select {
	case q <- j:
	case <-p.ctx.Done():
	}
}

// Close cancels in-flight pushes and waits for the workers; an interrupted
// copy keeps its pending/transferring state and is requeued at the next start.
func (p *Pool) Close() {
	p.mu.Lock()
	p.cancel()
	p.mu.Unlock()
	p.wg.Wait()
}

func (p *Pool) work(q chan Job) {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case j := <-q:
			p.run(j)
		}
	}
}

func (p *Pool) run(j Job) {
	unreachable := true
	for attempt := 1; ; attempt++ {
		p.settle(j, store.CopyTransferring, attempt, nil)
		err := p.push(p.ctx, j)
		if err == nil {
			p.settle(j, store.CopyVerified, attempt, nil)
			return
		}
		if p.ctx.Err() != nil {
			return
		}
		var ue unreachableError
		if !errors.As(err, &ue) {
			unreachable = false
		}
		var pe permanentError
		if errors.As(err, &pe) || attempt > len(p.delays) {
			state := store.CopyFailed
			if unreachable {
				state = store.CopyOffline
			}
			p.settle(j, state, attempt, err)
			return
		}
		p.settle(j, store.CopyPending, attempt, err)
		select {
		case <-p.ctx.Done():
			return
		case <-time.After(p.delays[attempt-1]):
		}
	}
}
