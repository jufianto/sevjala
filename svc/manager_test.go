package svc

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/user"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
)

func startAgent(t *testing.T, role string) (*Agent, string) {
	t.Helper()
	dir := shortDir(t)
	a := &Agent{DataDir: dir, Config: testConfig(role), Version: "test", pollEvery: 200 * time.Millisecond}
	if err := a.Start(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Stop(nil) })
	return a, dir
}

// relay sends method path through the node's /controller relay.
func relay(t *testing.T, nodeDir, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, "http://sevjala/controller"+path, rd)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Transport: localapi.NewTransport(localapi.Endpoint(nodeDir)), Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out: " + what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestManagerRunsControllerCommands(t *testing.T) {
	ctrl, ctrlDir := startAgent(t, config.RoleController)
	node, nodeDir := startAgent(t, config.RoleNode)
	ctx := context.Background()
	cc, nc := localapi.NewClient(localapi.Endpoint(ctrlDir)), localapi.NewClient(localapi.Endpoint(nodeDir))

	nodeSt, err := nc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	code, err := nc.Code(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cc.AddAgent(ctx, node.lan.Addr()); err != nil {
		t.Fatal(err)
	}
	if _, err := cc.Pair(ctx, nodeSt.AgentID, code.Code, nodeSt.FP); err != nil {
		t.Fatal(err)
	}

	eventually(t, "a poll with a hello", func() bool {
		st, err := nc.Status(ctx)
		return err == nil && st.Controller != nil && st.Controller.Hostname == ctrl.host
	})
	eventually(t, "the node learns it is not a manager", func() bool {
		c, out := relay(t, nodeDir, http.MethodGet, "/status", nil)
		return c == http.StatusForbidden && strings.Contains(out["error"].(string), "this PC is not a manager of")
	})

	if _, err := ctrl.ctrl.SetManager(logx.WithBy(localapi.WithAdmin(ctx, true), "admin"), nodeSt.AgentID, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the controller answers through the relay", func() bool {
		c, out := relay(t, nodeDir, http.MethodGet, "/status", nil)
		return c == http.StatusOK && out["role"] == config.RoleController
	})
	if st, _ := nc.Status(ctx); st.Controller == nil || !st.Controller.Manager || st.Controller.Hostname != ctrl.host || st.Controller.Links == 0 {
		t.Errorf("node status controller %+v", st.Controller)
	}

	if c, out := relay(t, nodeDir, http.MethodPut, "/db", map[string]string{"dumper": "/bin/sh"}); c != http.StatusForbidden ||
		out["code"] != "controller_admin_required" {
		t.Errorf("dumper through a manager: %d %v", c, out)
	}

	if c, out := relay(t, nodeDir, http.MethodPatch, "/policies/daily", map[string]int{"keep": 40}); c != http.StatusOK {
		t.Fatalf("policy change: %d %v", c, out)
	}
	me, _ := user.Current()
	host, _ := os.Hostname()
	es, err := logx.Tail(config.LogDir(ctrlDir), 200)
	if err != nil {
		t.Fatal(err)
	}
	var by string
	for _, e := range es {
		var r map[string]any
		_ = json.Unmarshal(e, &r)
		if r["event"] == logx.EventPolicyChanged {
			by, _ = r["by"].(string)
			break
		}
	}
	if !strings.HasSuffix(by, " via "+host) || !strings.Contains(by, me.Username) {
		t.Errorf("policy change by %q", by)
	}

	if _, err := ctrl.ctrl.SetManager(localapi.WithAdmin(ctx, true), nodeSt.AgentID, false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the relay refuses again", func() bool {
		c, _ := relay(t, nodeDir, http.MethodGet, "/status", nil)
		return c == http.StatusForbidden
	})
}

func TestRemovingAManagerTellsTheNode(t *testing.T) {
	ctrl, ctrlDir := startAgent(t, config.RoleController)
	node, nodeDir := startAgent(t, config.RoleNode)
	ctx := context.Background()
	cc, nc := localapi.NewClient(localapi.Endpoint(ctrlDir)), localapi.NewClient(localapi.Endpoint(nodeDir))
	nodeSt, _ := nc.Status(ctx)
	code, _ := nc.Code(ctx)
	if _, err := cc.AddAgent(ctx, node.lan.Addr()); err != nil {
		t.Fatal(err)
	}
	if _, err := cc.Pair(ctx, nodeSt.AgentID, code.Code, nodeSt.FP); err != nil {
		t.Fatal(err)
	}
	if _, err := ctrl.ctrl.SetManager(localapi.WithAdmin(ctx, true), nodeSt.AgentID, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the controller answers through the relay", func() bool {
		c, _ := relay(t, nodeDir, http.MethodGet, "/status", nil)
		return c == http.StatusOK
	})

	if _, err := cc.RemoveAgent(ctx, nodeSt.AgentID, false, ""); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	c, out := relay(t, nodeDir, http.MethodGet, "/status", nil)
	for c != http.StatusForbidden && time.Since(start) < 5*time.Second {
		time.Sleep(50 * time.Millisecond)
		c, out = relay(t, nodeDir, http.MethodGet, "/status", nil)
	}
	if c != http.StatusForbidden || !strings.Contains(out["error"].(string), "this PC is not a manager of") || time.Since(start) > 5*time.Second {
		t.Errorf("after removal, after %s: %d %v", time.Since(start).Round(time.Second), c, out)
	}
	if st, _ := nc.Status(ctx); st.Controller == nil || st.Controller.Manager {
		t.Errorf("node still a manager: %+v", st.Controller)
	}
}
