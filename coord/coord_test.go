package coord

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/jufianto/sevjala/localapi"
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
	ctrl := &Service{Controller: &Controller{}}
	if _, err := ctrl.Code(); !is409(err, "this agent is a controller") {
		t.Errorf("Code on a controller: %v", err)
	}
}

func is409(err error, msg string) bool {
	var e *localapi.Error
	return errors.As(err, &e) && e.Code == http.StatusConflict && strings.Contains(e.Msg, msg)
}
