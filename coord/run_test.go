package coord

import (
	"context"
	"testing"
	"time"

	"github.com/jufianto/sevjala/dump/dumptest"
)

func TestRunTicksAndStops(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	ctrl.PollEvery, ctrl.ReconcileEvery = 10*time.Millisecond, 20*time.Millisecond
	pairNode(t, ctrl, startAgent(t, "node", "PC-1"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		ctrl.Run(ctx)
		close(done)
	}()
	settled(t, ctrl, 1)
	if a := agentByHost(t, ctrl, "PC-1"); a.FreeBytes == 0 {
		t.Errorf("not polled: %+v", a)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}
