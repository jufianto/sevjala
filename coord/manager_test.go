package coord

import (
	"context"
	"testing"

	"github.com/jufianto/sevjala/dump/dumptest"
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
