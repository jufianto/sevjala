package coord

import (
	"context"
	"strings"
	"testing"

	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/store"
)

func agentByHost(t *testing.T, ctrl *Controller, host string) localapi.Agent {
	t.Helper()
	as, err := ctrl.Agents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range as {
		if a.Hostname == host {
			return a
		}
	}
	t.Fatalf("no agent %s in %+v", host, as)
	return localapi.Agent{}
}

func TestPollAllStates(t *testing.T) {
	ctx := context.Background()
	ctrl := newController(t)
	n := startAgent(t, "node", "PC-1")
	pairNode(t, ctrl, n)

	if !ctrl.PollAll(ctx) {
		t.Error("first answer is a node coming back")
	}
	a := agentByHost(t, ctrl, "PC-1")
	if a.State != store.AgentOnline || a.FreeBytes <= 0 || a.Problem != "" || a.LastSeen.IsZero() {
		t.Fatalf("after a poll %+v", a)
	}
	if ctrl.PollAll(ctx) {
		t.Error("an online node did not come back")
	}

	n.stop(t)
	for i := 1; i <= 3; i++ {
		ctrl.PollAll(ctx)
		a = agentByHost(t, ctrl, "PC-1")
		if want := map[bool]string{true: store.AgentOffline, false: store.AgentOnline}[i == 3]; a.State != want || a.Problem == "" {
			t.Fatalf("after %d missed polls %+v", i, a)
		}
	}

	n.restart(t, ctrl)
	if !ctrl.PollAll(ctx) {
		t.Error("an offline node answering did not count as coming back")
	}
	if a = agentByHost(t, ctrl, "PC-1"); a.State != store.AgentOnline || a.Problem != "" {
		t.Errorf("back %+v", a)
	}
}

func TestPollRejectedIsError(t *testing.T) {
	ctx := context.Background()
	ctrl := newController(t)
	n := startAgent(t, "node", "PC-1")
	pairNode(t, ctrl, n)
	ctrl.PollAll(ctx)
	other := newController(t)
	pairNode(t, other, n)
	ctrl.PollAll(ctx)
	if a := agentByHost(t, ctrl, "PC-1"); a.State != store.AgentError || !strings.Contains(a.Problem, "not paired") {
		t.Errorf("re-paired elsewhere %+v", a)
	}
}

func TestPollWarnsOnLowSpace(t *testing.T) {
	ctx := context.Background()
	ctrl := newController(t)
	n := startAgent(t, "node", "PC-1")
	pairNode(t, ctrl, n)
	n.stop(t)
	n.inv = mustInventory(t, n.dir, 1<<62)
	n.restart(t, ctrl)
	ctrl.PollAll(ctx)
	if a := agentByHost(t, ctrl, "PC-1"); a.State != store.AgentWarning || !strings.Contains(a.Problem, "below the floor") {
		t.Errorf("low space %+v", a)
	}
}
