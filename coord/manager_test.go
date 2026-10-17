package coord

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jufianto/sevjala/dump/dumptest"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
)

func TestAgentsShowManager(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	pc1 := startAgent(t, "node", "PC-1")
	pairNode(t, ctrl, pc1)
	if err := ctrl.Store.SetManager(context.Background(), pc1.id.AgentID, true); err != nil {
		t.Fatal(err)
	}
	as, err := ctrl.Agents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range as {
		if a.Manager != (a.AgentID == pc1.id.AgentID) {
			t.Errorf("%s manager %v", a.Hostname, a.Manager)
		}
	}
}

func TestSetManagerMarksANode(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	pc1 := startAgent(t, "node", "PC-1")
	pairNode(t, ctrl, pc1)
	var buf lockedBuffer
	ctrl.Log = logx.JSON(&buf)
	changed := 0
	ctrl.OnManagersChanged = func() { changed++ }
	ctx := logx.WithBy(asAdmin, "admin")

	a, err := ctrl.SetManager(ctx, "PC-1", true)
	if err != nil || !a.Manager || a.AgentID != pc1.id.AgentID {
		t.Fatalf("on: %+v %v", a, err)
	}
	if a, _ := ctrl.SetManager(ctx, "PC-1", false); a.Manager {
		t.Error("still a manager after off")
	}
	if changed != 2 {
		t.Errorf("OnManagersChanged ran %d times", changed)
	}
	var events []string
	for _, r := range buf.records(t) {
		if r["event"] == logx.EventManagerSet {
			events = append(events, fmt.Sprint(r["hostname"], " ", r["manager"], " ", r["by"]))
		}
	}
	if strings.Join(events, "|") != "PC-1 true admin|PC-1 false admin" {
		t.Errorf("events %q", events)
	}
}

func TestSetManagerRefusesTheController(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	_, err := ctrl.SetManager(context.Background(), ctrl.Self.AgentID, true)
	if errCode(err) != http.StatusConflict || !strings.Contains(err.Error(), "only a backup PC can be a manager") {
		t.Errorf("err %v", err)
	}
}

func TestSetManagerNeedsAdmin(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	pc1 := startAgent(t, "node", "PC-1")
	pairNode(t, ctrl, pc1)
	svc := &Service{Controller: ctrl}
	if _, err := svc.SetManager(asUser, "PC-1", true); !localapi.IsAdminRequired(err) {
		t.Errorf("normal user: %v", err)
	}
	if _, err := svc.SetManager(asAdmin, "PC-1", true); err != nil {
		t.Errorf("admin: %v", err)
	}
}
