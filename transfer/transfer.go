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
	Attempts int // made before this job: a restart keeps counting
}

// Update is what the pool reports about a job: the copy's state, the attempts
// so far, the error of the last attempt, and the wait before the next one.
type Update struct {
	State    string
	Attempts int
	Err      error
	Retry    time.Duration
}

// DefaultDelays are the waits before retries 1-3; after them a copy is failed
// (or offline) until reconciliation sends it again.
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
	settle func(Job, Update)
	delays []time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	queues map[string]chan Job
	queued map[[2]string]bool // backup, agent: queued or running
}

func NewPool(push func(context.Context, Job) error, settle func(Job, Update), delays []time.Duration) *Pool {
	ctx, cancel := context.WithCancel(context.Background())
	return &Pool{push: push, settle: settle, delays: delays, ctx: ctx, cancel: cancel, queues: map[string]chan Job{},
		queued: map[[2]string]bool{}}
}

// Enqueue drops a job whose copy is already queued or running.
func (p *Pool) Enqueue(j Job) {
	p.mu.Lock()
	key := [2]string{j.BackupID, j.AgentID}
	if p.ctx.Err() != nil || p.queued[key] {
		p.mu.Unlock()
		return
	}
	p.queued[key] = true
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
	done := func(u Update) {
		p.mu.Lock()
		delete(p.queued, [2]string{j.BackupID, j.AgentID})
		p.mu.Unlock()
		if u.State != "" {
			p.settle(j, u)
		}
	}
	unreachable := true
	for try := 1; ; try++ {
		n := j.Attempts + try
		p.settle(j, Update{State: store.CopyTransferring, Attempts: n})
		err := p.push(p.ctx, j)
		if err == nil {
			done(Update{State: store.CopyVerified, Attempts: n})
			return
		}
		if p.ctx.Err() != nil {
			done(Update{})
			return
		}
		var ue unreachableError
		if !errors.As(err, &ue) {
			unreachable = false
		}
		var pe permanentError
		if errors.As(err, &pe) || try > len(p.delays) {
			state := store.CopyFailed
			if unreachable {
				state = store.CopyOffline
			}
			done(Update{State: state, Attempts: n, Err: err})
			return
		}
		wait := p.delays[try-1]
		p.settle(j, Update{State: store.CopyPending, Attempts: n, Err: err, Retry: wait})
		select {
		case <-p.ctx.Done():
			done(Update{})
			return
		case <-time.After(wait):
		}
	}
}
