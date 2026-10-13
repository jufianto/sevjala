package coord

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/lanapi"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/store"
	"github.com/jufianto/sevjala/transfer"
)

type item struct{ id, host string }

func key(i item) (string, string) { return i.id, i.host }

func TestResolve(t *testing.T) {
	items := []item{
		{"3f2a9c1e-1111-4111-8111-111111111111", "BACKUP-PC-01"},
		{"3f2a0000-2222-4222-8222-222222222222", "BACKUP-PC-02"},
		{"9a000000-3333-4333-8333-333333333333", "SAMENAME"},
		{"9b000000-4444-4444-8444-444444444444", "samename"},
	}
	tests := []struct {
		name, q, want string
		code          int
	}{
		{"exact id", "3f2a9c1e-1111-4111-8111-111111111111", "BACKUP-PC-01", 0},
		{"hostname any case", "backup-pc-02", "BACKUP-PC-02", 0},
		{"unique prefix", "3f2a9", "BACKUP-PC-01", 0},
		{"prefix upper case", "3F2A0", "BACKUP-PC-02", 0},
		{"ambiguous prefix", "3f2a", "", http.StatusConflict},
		{"ambiguous hostname", "SameName", "", http.StatusConflict},
		{"short prefix", "9a0", "", http.StatusNotFound},
		{"none", "nope", "", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolve(items, tt.q, key, "hint")
			if tt.code == 0 {
				if err != nil || got.host != tt.want {
					t.Fatalf("got %+v, %v", got, err)
				}
				return
			}
			var e *localapi.Error
			if !errors.As(err, &e) || e.Code != tt.code {
				t.Fatalf("err = %#v, want code %d", err, tt.code)
			}
			if tt.code == http.StatusConflict && !strings.Contains(e.Msg, "9a000000") && !strings.Contains(e.Msg, "3f2a9c1e") {
				t.Errorf("ambiguity does not list candidates: %s", e.Msg)
			}
		})
	}
}

func TestWrongRoleIs409(t *testing.T) {
	node := &Service{Node: &Node{}}
	if _, err := node.Discover(context.Background()); !is409(err, "this agent is a node") {
		t.Errorf("Discover on a node: %v", err)
	}
	if _, err := node.BackupNow(context.Background(), "", nil); !is409(err, "this agent is a node") {
		t.Errorf("BackupNow on a node: %v", err)
	}
	if _, err := node.DetectDatabase(context.Background()); !is409(err, "this agent is a node") {
		t.Errorf("DetectDatabase on a node: %v", err)
	}
	ctrl := &Service{Controller: &Controller{}}
	if _, err := ctrl.Code(); !is409(err, "this agent is a controller") {
		t.Errorf("Code on a controller: %v", err)
	}
}

func is409(err error, msg string) bool {
	var e *localapi.Error
	return errors.As(err, &e) && e.Code == http.StatusConflict && strings.Contains(e.Msg, msg)
}

// The pool's reaction to classify's result: a permanent error ends the copy
// after one attempt, an unreachable node ends it offline after the retries.
func TestClassifyPushErrors(t *testing.T) {
	dial := &net.OpError{Op: "dial", Err: errors.New("connection refused")}
	tests := []struct {
		name, want string
		err        error
	}{
		{"conflict", "failed 1", &lanapi.HTTPError{Code: http.StatusConflict}},
		{"bad request", "failed 1", &lanapi.HTTPError{Code: http.StatusBadRequest}},
		{"forbidden", "failed 1", &lanapi.HTTPError{Code: http.StatusForbidden}},
		{"no space", "failed 1", &lanapi.HTTPError{Code: http.StatusInsufficientStorage}},
		{"rejected at TLS", "failed 1", fmt.Errorf("x %w", lanapi.ErrRejected)},
		{"node certificate changed", "failed 1", fmt.Errorf("x %w", lanapi.ErrPinMismatch)},
		{"mismatch is retried", "failed 3", &lanapi.HTTPError{Code: http.StatusUnprocessableEntity}},
		{"busy is retried", "failed 3", &lanapi.HTTPError{Code: http.StatusServiceUnavailable}},
		{"dial", "offline 3", fmt.Errorf("put: %w", dial)},
		{"other", "failed 3", errors.New("EOF")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan string, 1)
			p := transfer.NewPool(func(context.Context, transfer.Job) error { return classify(tt.err) },
				func(_ transfer.Job, u transfer.Update) {
					if u.State == store.CopyFailed || u.State == store.CopyOffline {
						if !errors.Is(u.Err, tt.err) {
							t.Errorf("classify lost the error: %v", u.Err)
						}
						done <- fmt.Sprintf("%s %d", u.State, u.Attempts)
					}
				}, []time.Duration{time.Millisecond, time.Millisecond})
			defer p.Close()
			p.Enqueue(transfer.Job{BackupID: "b", AgentID: "n"})
			if got := <-done; got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}
