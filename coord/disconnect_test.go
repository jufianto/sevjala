package coord

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
)

const (
	currentCtrl = "c960acf5-1111-4111-8111-111111111111"
	formerCtrl  = "0d1d0000-2222-4222-8222-222222222222"
)

type nodeFixture struct {
	svc    *Service
	inv    *inventory.Store
	log    *lockedBuffer
	forgot bool
	status localapi.Status
}

// newNodeFixture is a backup PC paired with SERVER-01 holding two of its
// backups and two from OLD-PC, a controller it was paired with before.
func newNodeFixture(t *testing.T) *nodeFixture {
	t.Helper()
	inv, err := inventory.Open(t.TempDir(), 0, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	f := &nodeFixture{inv: inv, log: &lockedBuffer{},
		status: localapi.Status{Role: "node", Hostname: "BACKUP-PC-1", Controller: &localapi.Peer{AgentID: currentCtrl, Hostname: "SERVER-01"}}}
	for i, src := range []struct{ id, host string }{{formerCtrl, "OLD-PC"}, {formerCtrl, "OLD-PC"}, {currentCtrl, "SERVER-01"}, {currentCtrl, "SERVER-01"}} {
		data := bytes.Repeat([]byte("x"), 1000*(i+1))
		sum := sha256.Sum256(data)
		m := inventory.Manifest{V: 1, BackupID: fmt.Sprintf("BK-20261014-00%d000-000%d", i, i), Database: "shop",
			CreatedAt: time.Date(2026, 10, 14, i, 0, 0, 0, time.UTC), SourceAgent: src.id, SourceHost: src.host,
			Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Tags: []string{"daily"}}
		if err := inv.Receive(m, m.SHA256, m.Size, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	f.svc = &Service{StatusFunc: func() localapi.Status { return f.status },
		Node: &Node{Store: inv, Log: logx.JSON(f.log), Forget: func() error { f.forgot = true; return nil }}}
	return f
}

func (f *nodeFixture) held(t *testing.T) []string {
	t.Helper()
	ms, err := inventory.List(f.inv.Dir())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range ms {
		ids = append(ids, m.BackupID)
	}
	return ids
}

func TestDisconnect(t *testing.T) {
	f := newNodeFixture(t)
	if _, err := f.svc.Disconnect(asUser, ""); !localapi.IsAdminRequired(err) {
		t.Errorf("normal user: %v", err)
	}
	_, err := f.svc.Disconnect(asAdmin, "")
	e, ok := localapi.AsConfirm(err)
	if !ok || e.Word != "BACKUP-PC-1" || !strings.Contains(e.Msg, "forgets its controller SERVER-01") ||
		!strings.Contains(e.Msg, "its 4 backups stay") || f.forgot {
		t.Fatalf("without the word: %v (forgot %v)", err, f.forgot)
	}
	d, err := f.svc.Disconnect(logx.WithBy(asAdmin, "admin"), "BACKUP-PC-1")
	if err != nil || d.Controller != "SERVER-01" || d.Backups != 4 || !f.forgot {
		t.Fatalf("disconnect: %+v %v (forgot %v)", d, err, f.forgot)
	}
	var events []string
	for _, r := range f.log.records(t) {
		if r["event"] == logx.EventControllerDisconnected {
			events = append(events, fmt.Sprint(r["controller_id"], "/", r["by"]))
		}
	}
	if strings.Join(events, "|") != currentCtrl+"/admin" {
		t.Errorf("events %q", events)
	}

	f.status.Controller = nil
	if _, err := f.svc.Disconnect(asAdmin, "BACKUP-PC-1"); errCode(err) != http.StatusConflict ||
		!strings.Contains(err.Error(), "not paired with a controller") {
		t.Errorf("not paired: %v", err)
	}
	if _, err := (&Service{Controller: &Controller{}}).Disconnect(asAdmin, ""); !errors.Is(err, errNotNode) {
		t.Errorf("on a controller: %v", err)
	}
}

func TestRemoveHeld(t *testing.T) {
	for _, tc := range []struct {
		name    string
		req     localapi.RemoveHeld
		removed []string
		bytes   int64
		resend  int
	}{
		{"one id", localapi.RemoveHeld{IDs: []string{"BK-20261014-002000-0002"}}, []string{"BK-20261014-002000-0002"}, 3000, 1},
		{"a former controller by name", localapi.RemoveHeld{From: "old-pc"}, []string{"BK-20261014-000000-0000", "BK-20261014-001000-0001"}, 3000, 0},
		{"the current one by id prefix", localapi.RemoveHeld{From: "c960"}, []string{"BK-20261014-002000-0002", "BK-20261014-003000-0003"}, 7000, 2},
		{"all", localapi.RemoveHeld{All: true}, []string{"BK-20261014-000000-0000", "BK-20261014-001000-0001", "BK-20261014-002000-0002", "BK-20261014-003000-0003"}, 10000, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newNodeFixture(t)
			if _, err := f.svc.RemoveHeld(asUser, tc.req); !localapi.IsAdminRequired(err) {
				t.Errorf("normal user: %v", err)
			}
			_, err := f.svc.RemoveHeld(asAdmin, tc.req)
			e, ok := localapi.AsConfirm(err)
			if !ok || e.Word != "BACKUP-PC-1" || e.Deletes != len(tc.removed) || len(f.held(t)) != 4 {
				t.Fatalf("without the word: %v", err)
			}
			req := tc.req
			req.Confirm = "BACKUP-PC-1"
			r, err := f.svc.RemoveHeld(logx.WithBy(asAdmin, "admin"), req)
			if err != nil || !slices.Equal(r.Removed, tc.removed) || r.Bytes != tc.bytes || r.Resend != tc.resend || len(r.Failed) != 0 {
				t.Fatalf("removed %+v, %v", r, err)
			}
			if left := f.held(t); len(left) != 4-len(tc.removed) || slices.ContainsFunc(left, func(id string) bool { return slices.Contains(tc.removed, id) }) {
				t.Errorf("left %v", left)
			}
			n := 0
			for _, rec := range f.log.records(t) {
				if rec["event"] == logx.EventBackupDeleted && rec["reason"] == "removed on this PC by its operator" && rec["by"] == "admin" {
					n++
				}
			}
			if n != len(tc.removed) {
				t.Errorf("%d delete events", n)
			}
		})
	}
}

func TestRemoveHeldRefuses(t *testing.T) {
	f := newNodeFixture(t)
	for _, tc := range []struct {
		req  localapi.RemoveHeld
		code int
		want string
	}{
		{localapi.RemoveHeld{}, http.StatusBadRequest, "give backup ids, a controller (--from) or --all"},
		{localapi.RemoveHeld{All: true, From: "old-pc"}, http.StatusBadRequest, "give backup ids, a controller (--from) or --all"},
		{localapi.RemoveHeld{IDs: []string{"nope"}}, http.StatusBadRequest, `"nope" is not a backup id`},
		{localapi.RemoveHeld{IDs: []string{"BK-20991231-000000-FFFF"}}, http.StatusNotFound, "BK-20991231-000000-FFFF is not on this PC"},
		{localapi.RemoveHeld{From: "nobody"}, http.StatusNotFound, `no agent matches "nobody"; this PC holds backups from OLD-PC, SERVER-01`},
	} {
		if _, err := f.svc.RemoveHeld(asAdmin, tc.req); errCode(err) != tc.code || !strings.Contains(fmt.Sprint(err), tc.want) {
			t.Errorf("%+v: %v", tc.req, err)
		}
	}
	if len(f.held(t)) != 4 {
		t.Error("a refused request removed something")
	}
	if _, err := (&Service{Controller: &Controller{}}).RemoveHeld(asAdmin, localapi.RemoveHeld{All: true}); !errors.Is(err, errNotNode) {
		t.Errorf("on a controller: %v", err)
	}
}
