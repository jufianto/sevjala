package coord

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jufianto/sevjala/dump/dumptest"
	"github.com/jufianto/sevjala/logx"
)

func TestSetNameRenamesABackupPC(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	pc1 := startAgent(t, "node", "PC-1")
	pairNode(t, ctrl, pc1)
	var buf lockedBuffer
	ctrl.Log = logx.JSON(&buf)
	ctx := logx.WithBy(asUser, "user")

	a, err := ctrl.SetName(ctx, "PC-1", "  Office PC ")
	if err != nil || a.Hostname != "Office PC" || a.ComputerName != "PC-1" || a.AgentID != pc1.id.AgentID {
		t.Fatalf("named: %+v %v", a, err)
	}
	if a, err := ctrl.Ping(ctx, "office pc"); err != nil || a.Agent.AgentID != pc1.id.AgentID {
		t.Errorf("the new name does not pick the PC: %+v %v", a, err)
	}
	a, err = ctrl.SetName(ctx, "Office PC", "")
	if err != nil || a.Hostname != "PC-1" || a.ComputerName != "" {
		t.Errorf("cleared: %+v %v", a, err)
	}
	var events []string
	for _, r := range buf.records(t) {
		if r["event"] == logx.EventAgentRenamed {
			events = append(events, fmt.Sprint(r["name"], "/", r["by"]))
		}
	}
	if strings.Join(events, "|") != "Office PC/user|/user" {
		t.Errorf("events %q", events)
	}
}

func TestSetNameRefuses(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	pc1, pc2 := startAgent(t, "node", "PC-1"), startAgent(t, "node", "PC-2")
	pairNode(t, ctrl, pc1)
	pairNode(t, ctrl, pc2)
	ctx := context.Background()
	if _, err := ctrl.SetName(ctx, "PC-2", "Stock room"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		agent, name string
		code        int
		want        string
	}{
		{"PC-1", "pc-2", http.StatusConflict, `"pc-2" is the computer name of Stock room`},
		{"PC-1", "STOCK ROOM", http.StatusConflict, `"STOCK ROOM" is already the name of Stock room`},
		{"PC-1", "server-01", http.StatusConflict, `"server-01" is already the name of SERVER-01`},
		{"PC-1", strings.Repeat("x", 41), http.StatusBadRequest, "at most 40 characters"},
		{"PC-1", "a\tb", http.StatusBadRequest, "letters, digits, spaces and punctuation"},
		{ctrl.Self.AgentID, "Main", http.StatusConflict, "only a backup PC can be renamed"},
		{"SERVER-01", "Main", http.StatusConflict, "only a backup PC can be renamed"},
		{"nope", "x", http.StatusNotFound, "no agent matches"},
	} {
		if _, err := ctrl.SetName(ctx, tc.agent, tc.name); errCode(err) != tc.code || !strings.Contains(fmt.Sprint(err), tc.want) {
			t.Errorf("%s → %q: %v", tc.agent, tc.name, err)
		}
	}
	if a, err := ctrl.SetName(ctx, "PC-1", "PC-1"); err != nil || a.Hostname != "PC-1" {
		t.Errorf("its own computer name: %+v %v", a, err)
	}
}
