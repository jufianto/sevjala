package svc

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/store"
)

func shortDir(t *testing.T) string {
	t.Helper()
	base := os.TempDir()
	if runtime.GOOS != "windows" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "sj")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// testConfig keeps tests off the real ports and off other interfaces.
func testConfig(role string) config.Config {
	c := config.Default(role)
	c.Listen = "127.0.0.1:0"
	c.DiscoveryPort = 0
	return c
}

func TestAgentStartServesStatusAndLogs(t *testing.T) {
	dir := shortDir(t)
	a := &Agent{DataDir: dir, Config: testConfig(config.RoleNode), Version: "test"}
	if err := a.Start(nil); err != nil {
		t.Fatal(err)
	}

	st, err := localapi.NewClient(localapi.Endpoint(dir)).Status(context.Background())
	if err != nil {
		a.Stop(nil)
		t.Fatal(err)
	}
	if st.Role != config.RoleNode || st.Version != "test" || st.PID != os.Getpid() || st.DataDir != dir {
		t.Errorf("status = %+v", st)
	}
	if st.AgentID == "" || !strings.HasPrefix(st.FP, "sha256:") {
		t.Errorf("status lacks identity: %+v", st)
	}
	if _, err := localapi.NewClient(localapi.Endpoint(dir)).Discover(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "this agent is a node") {
		t.Errorf("discover on a node: %v", err)
	}

	if err := a.Stop(nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(config.LogDir(dir), logx.FileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{logx.EventAgentStarted, logx.EventAgentStopped, logx.EventIdentityCreated} {
		if !strings.Contains(string(b), `"event":"`+ev+`"`) {
			t.Errorf("log lacks %s: %s", ev, b)
		}
	}
}

func TestAgentStartFailsWhenEndpointTaken(t *testing.T) {
	dir := shortDir(t)
	first := &Agent{DataDir: dir, Config: testConfig(config.RoleNode)}
	if err := first.Start(nil); err != nil {
		t.Fatal(err)
	}
	defer first.Stop(nil)
	second := &Agent{DataDir: dir, Config: testConfig(config.RoleNode)}
	if err := second.Start(nil); err == nil {
		second.Stop(nil)
		t.Fatal("second agent started on a taken endpoint")
	}
}

func TestNewServiceConfig(t *testing.T) {
	s, err := New("/data", &Agent{})
	if err != nil {
		t.Fatal(err)
	}
	if s.String() != "Sevjala Agent" {
		t.Errorf("display name = %q", s.String())
	}
}

func TestControllerAgentOpensStore(t *testing.T) {
	dir := shortDir(t)
	a := &Agent{DataDir: dir, Config: testConfig(config.RoleController), Version: "test"}
	if err := a.Start(nil); err != nil {
		t.Fatal(err)
	}
	defer a.Stop(nil)
	c := localapi.NewClient(localapi.Endpoint(dir))
	if as, err := c.Agents(context.Background()); err != nil || len(as) != 0 {
		t.Errorf("agents: %+v %v", as, err)
	}
	if _, err := os.Stat(filepath.Join(dir, store.FileName)); err != nil {
		t.Errorf("controller.db: %v", err)
	}
	if _, err := c.Code(context.Background()); err == nil || !strings.Contains(err.Error(), "this agent is a controller") {
		t.Errorf("code on a controller: %v", err)
	}
}
