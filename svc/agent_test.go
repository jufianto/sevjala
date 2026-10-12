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

func TestAgentStartServesStatusAndLogs(t *testing.T) {
	dir := shortDir(t)
	a := &Agent{DataDir: dir, Config: config.Default(config.RoleNode), Version: "test"}
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

	if err := a.Stop(nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(config.LogDir(dir), logx.FileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{logx.EventAgentStarted, logx.EventAgentStopped} {
		if !strings.Contains(string(b), `"event":"`+ev+`"`) {
			t.Errorf("log lacks %s: %s", ev, b)
		}
	}
}

func TestAgentStartFailsWhenEndpointTaken(t *testing.T) {
	dir := shortDir(t)
	first := &Agent{DataDir: dir, Config: config.Default(config.RoleNode)}
	if err := first.Start(nil); err != nil {
		t.Fatal(err)
	}
	defer first.Stop(nil)
	second := &Agent{DataDir: dir, Config: config.Default(config.RoleNode)}
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
