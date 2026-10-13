package transfer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jufianto/sevjala/store"
)

var fast = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}

type recorder struct {
	mu     sync.Mutex
	events []string
	done   chan Job
}

func newRecorder() *recorder { return &recorder{done: make(chan Job, 16)} }

func (r *recorder) settle(j Job, state string, attempts int, _ error) {
	r.mu.Lock()
	r.events = append(r.events, fmt.Sprintf("%s %s %d", j.AgentID, state, attempts))
	r.mu.Unlock()
	if state == store.CopyVerified || state == store.CopyFailed || state == store.CopyOffline {
		r.done <- j
	}
}

func (r *recorder) wait(t *testing.T) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not settle")
	}
}

func (r *recorder) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

func run(t *testing.T, push func(context.Context, Job) error) []string {
	r := newRecorder()
	p := NewPool(push, r.settle, fast)
	defer p.Close()
	p.Enqueue(Job{BackupID: "b1", AgentID: "n1"})
	r.wait(t)
	return r.got()
}

func failing(errs ...error) func(context.Context, Job) error {
	var mu sync.Mutex
	i := 0
	return func(context.Context, Job) error {
		mu.Lock()
		defer mu.Unlock()
		if i < len(errs) {
			i++
			return errs[i-1]
		}
		return nil
	}
}

func TestOutcomes(t *testing.T) {
	boom := errors.New("500 internal")
	refused := Unreachable(errors.New("connection refused"))
	tests := []struct {
		name string
		push func(context.Context, Job) error
		want []string
	}{
		{"first try", failing(), []string{"n1 transferring 1", "n1 verified 1"}},
		{"retry then success", failing(boom, boom),
			[]string{"n1 transferring 1", "n1 pending 1", "n1 transferring 2", "n1 pending 2", "n1 transferring 3", "n1 verified 3"}},
		{"retries exhausted", failing(boom, boom, boom, boom), []string{"n1 transferring 1", "n1 pending 1", "n1 transferring 2",
			"n1 pending 2", "n1 transferring 3", "n1 pending 3", "n1 transferring 4", "n1 failed 4"}},
		{"never reachable is offline", failing(refused, refused, refused, refused), []string{"n1 transferring 1", "n1 pending 1",
			"n1 transferring 2", "n1 pending 2", "n1 transferring 3", "n1 pending 3", "n1 transferring 4", "n1 offline 4"}},
		{"reached once is failed", failing(refused, boom, refused, refused), []string{"n1 transferring 1", "n1 pending 1",
			"n1 transferring 2", "n1 pending 2", "n1 transferring 3", "n1 pending 3", "n1 transferring 4", "n1 failed 4"}},
		{"permanent is not retried", failing(Permanent(boom)), []string{"n1 transferring 1", "n1 failed 1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(t, tt.push); !slices.Equal(got, tt.want) {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestCloseCancelsInFlight(t *testing.T) {
	r := newRecorder()
	started := make(chan struct{})
	p := NewPool(func(ctx context.Context, _ Job) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}, r.settle, fast)
	p.Enqueue(Job{BackupID: "b1", AgentID: "n1"})
	<-started
	p.Close()
	if got := r.got(); !slices.Equal(got, []string{"n1 transferring 1"}) {
		t.Errorf("events %q", got)
	}
	p.Enqueue(Job{BackupID: "b2", AgentID: "n2"})
}

func TestOneWorkerPerNodeInOrder(t *testing.T) {
	r := newRecorder()
	var mu sync.Mutex
	var order []string
	p := NewPool(func(_ context.Context, j Job) error {
		mu.Lock()
		order = append(order, j.AgentID+":"+j.BackupID)
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		return nil
	}, r.settle, fast)
	defer p.Close()
	for _, j := range []Job{{"a", "n1"}, {"b", "n1"}, {"c", "n2"}} {
		p.Enqueue(j)
	}
	for range 3 {
		r.wait(t)
	}
	mu.Lock()
	defer mu.Unlock()
	if slices.Index(order, "n1:a") > slices.Index(order, "n1:b") {
		t.Errorf("order %q", order)
	}
}
