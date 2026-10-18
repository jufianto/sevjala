package svc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/inventory"
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
	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h := st.Health; h == nil || h.Level != localapi.HealthProblem || !strings.HasPrefix(h.Headline, "Not protected") {
		t.Errorf("health %+v", h)
	}
}

func TestNodeAgentServesItsStore(t *testing.T) {
	dir := shortDir(t)
	a := &Agent{DataDir: dir, Config: testConfig(config.RoleNode), Version: "test"}
	if err := a.Start(nil); err != nil {
		t.Fatal(err)
	}
	defer a.Stop(nil)
	st, err := localapi.NewClient(localapi.Endpoint(dir)).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Store == nil || st.Store.Path != filepath.Join(dir, "store") || st.Store.FreeBytes <= 0 {
		t.Errorf("store %+v", st.Store)
	}
	if h := st.Health; h == nil || h.Level != localapi.HealthWarning || !strings.HasPrefix(h.Headline, "Not paired") || h.FreeBytes != st.Store.FreeBytes {
		t.Errorf("health %+v", h)
	}
	if host, _ := os.Hostname(); st.Hostname != host {
		t.Errorf("hostname %q", st.Hostname)
	}
}

func TestControllerStartRemovesUnfinishedDumps(t *testing.T) {
	dir := shortDir(t)
	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(staging, "BK-20261013-101500-A8F2.part")
	_ = os.WriteFile(part, []byte("x"), 0o600)
	a := &Agent{DataDir: dir, Config: testConfig(config.RoleController), Version: "test"}
	if err := a.Start(nil); err != nil {
		t.Fatal(err)
	}
	defer a.Stop(nil)
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Errorf("unfinished dump kept: %v", err)
	}
	bs, err := localapi.NewClient(localapi.Endpoint(dir)).Backups(context.Background(), false)
	if err != nil || len(bs) != 0 {
		t.Errorf("backups %+v, %v", bs, err)
	}
}

// A node names the controller it is paired with, and the controllers its
// backups came from, including one a later pairing replaced.
func TestNodeAgentNamesItsControllers(t *testing.T) {
	dir := shortDir(t)
	paired := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	if err := identity.SaveController(dir, identity.Peer{AgentID: "c960acf5", FP: strings.Repeat("ab", 32), PairedAt: paired}); err != nil {
		t.Fatal(err)
	}
	storeDir := filepath.Join(dir, "store")
	if err := os.MkdirAll(storeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, b := range []struct{ id, agent, host string }{
		{"BK-20261001-010000-0001", "old00001", "OLD-PC"},
		{"BK-20261015-010000-0002", "c960acf5", "SERVER-01"},
	} {
		data := []byte("-- " + b.id)
		sum := sha256.Sum256(data)
		m := inventory.Manifest{V: 1, BackupID: b.id, Database: "shop", CreatedAt: paired, SourceAgent: b.agent, SourceHost: b.host,
			Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Tags: []string{"daily"}}
		if err := os.WriteFile(filepath.Join(storeDir, b.id+inventory.DataExt), data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := inventory.WriteManifest(filepath.Join(storeDir, b.id+inventory.ManifestExt), m); err != nil {
			t.Fatal(err)
		}
	}
	a := &Agent{DataDir: dir, Config: testConfig(config.RoleNode), Version: "test"}
	if err := a.Start(nil); err != nil {
		t.Fatal(err)
	}
	defer a.Stop(nil)
	st, err := localapi.NewClient(localapi.Endpoint(dir)).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := &localapi.Peer{AgentID: "c960acf5", Hostname: "SERVER-01", PairedAt: paired}
	if st.Controller == nil || *st.Controller != *want {
		t.Errorf("controller %+v", st.Controller)
	}
	if st.Store == nil || !reflect.DeepEqual(st.Store.Sources, []inventory.Source{
		{AgentID: "c960acf5", Hostname: "SERVER-01", Backups: 1, Current: true},
		{AgentID: "old00001", Hostname: "OLD-PC", Backups: 1},
	}) {
		t.Errorf("store %+v", st.Store)
	}
	if want := int64(2 * len("-- BK-20261001-010000-0001")); st.Store.Bytes != want {
		t.Errorf("store bytes %d, want %d", st.Store.Bytes, want)
	}
}
