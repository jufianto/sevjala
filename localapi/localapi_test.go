package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// testDir is short on purpose: macOS caps Unix socket paths at 104 bytes and
// t.TempDir() under /var/folders often exceeds it.
func testDir(t *testing.T) string {
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

var discard = slog.New(slog.DiscardHandler)

type fakeBackend struct {
	status Status
	err    error

	mu                                sync.Mutex
	gotAgent, gotCode, gotAddr, gotFP string
	gotSettings                       DBSettings
	gotDB                             string
	gotTags                           []string
	gotName                           string
	gotPolicy                         PolicyChange
}

func (f *fakeBackend) Status() Status { return f.status }

func (f *fakeBackend) Code() (Code, error) {
	return Code{Code: "123456", AgentID: "n1", ExpiresAt: time.Date(2026, 10, 12, 16, 42, 0, 0, time.UTC)}, f.err
}

func (f *fakeBackend) Discover(context.Context) ([]Discovered, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []Discovered{{AgentID: "n1", Addr: "10.0.0.2:17891", Paired: true}}, nil
}

func (f *fakeBackend) AddAgent(_ context.Context, addr string) (Discovered, error) {
	f.mu.Lock()
	f.gotAddr = addr
	f.mu.Unlock()
	return Discovered{AgentID: "n1", Addr: addr}, f.err
}

func (f *fakeBackend) Pair(_ context.Context, agent, code, fp string) (Agent, error) {
	f.mu.Lock()
	f.gotAgent, f.gotCode, f.gotFP = agent, code, fp
	f.mu.Unlock()
	return Agent{AgentID: agent}, f.err
}

func (f *fakeBackend) Agents(context.Context) ([]Agent, error) { return nil, f.err }

func (f *fakeBackend) Ping(_ context.Context, agent string) (Ping, error) {
	f.mu.Lock()
	f.gotAgent = agent
	f.mu.Unlock()
	return Ping{Agent: Agent{AgentID: agent}, RTTMs: 3, Health: "healthy"}, f.err
}

func startBackend(t *testing.T, b Backend) string {
	t.Helper()
	ep := Endpoint(testDir(t))
	srv, err := Start(ep, NewHandler(b), discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown(context.Background()) })
	return ep
}

func startTest(t *testing.T, st Status) string {
	t.Helper()
	return startBackend(t, &fakeBackend{status: st})
}

func TestStatusRoundTrip(t *testing.T) {
	want := Status{Version: "0.1.0", Role: "node", PID: 42, UptimeS: 7, DataDir: "/x", AgentID: "a1", FP: "sha256:ab"}
	ep := startTest(t, want)
	got, err := NewClient(ep).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestUnknownPathIs404JSON(t *testing.T) {
	ep := startTest(t, Status{})
	resp, err := NewClient(ep).hc.Get("http://sevjala/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var m map[string]string
	if resp.StatusCode != http.StatusNotFound || json.Unmarshal(body, &m) != nil || m["error"] != "not found" {
		t.Errorf("got %d %q", resp.StatusCode, body)
	}
}

func TestSecondListenerFails(t *testing.T) {
	ep := startTest(t, Status{})
	if srv, err := Start(ep, NewHandler(&fakeBackend{}), discard); err == nil {
		srv.Shutdown(context.Background())
		t.Fatal("second Start on the same endpoint succeeded")
	} else if !strings.Contains(err.Error(), ep) {
		t.Errorf("error %q does not name the endpoint", err)
	}
}

func TestClientWithoutServerFails(t *testing.T) {
	if _, err := NewClient(Endpoint(testDir(t))).Status(context.Background()); err == nil {
		t.Fatal("Status succeeded with no server")
	}
}

func TestEndpointMovesWithCustomDataDir(t *testing.T) {
	a, b := Endpoint(testDir(t)), Endpoint(testDir(t))
	if a == defaultEndpoint || a == b {
		t.Errorf("custom endpoints %q, %q must differ from each other and from %q", a, b, defaultEndpoint)
	}
}

func TestRoutesRoundTrip(t *testing.T) {
	f := &fakeBackend{}
	c := NewClient(startBackend(t, f))
	ctx := context.Background()

	if code, err := c.Code(ctx); err != nil || code.Code != "123456" || code.AgentID != "n1" {
		t.Errorf("Code: %+v %v", code, err)
	}
	if ds, err := c.Discover(ctx); err != nil || len(ds) != 1 || !ds[0].Paired {
		t.Errorf("Discover: %+v %v", ds, err)
	}
	if d, err := c.AddAgent(ctx, "10.0.0.9:17891"); err != nil || d.Addr != "10.0.0.9:17891" {
		t.Errorf("AddAgent: %+v %v", d, err)
	}
	if a, err := c.Pair(ctx, "3f2a", "654321", "3f2a9b1c77de0a41"); err != nil || a.AgentID != "3f2a" {
		t.Errorf("Pair: %+v %v", a, err)
	}
	f.mu.Lock()
	if f.gotAgent != "3f2a" || f.gotCode != "654321" || f.gotFP != "3f2a9b1c77de0a41" || f.gotAddr != "10.0.0.9:17891" {
		t.Errorf("backend got agent=%q code=%q fp=%q addr=%q", f.gotAgent, f.gotCode, f.gotFP, f.gotAddr)
	}
	f.mu.Unlock()
	if as, err := c.Agents(ctx); err != nil || as == nil || len(as) != 0 {
		t.Errorf("Agents: %#v %v (want an empty, non-nil list)", as, err)
	}
	if p, err := c.Ping(ctx, "BACKUP PC/1"); err != nil || p.Agent.AgentID != "BACKUP PC/1" || p.RTTMs != 3 {
		t.Errorf("Ping: %+v %v", p, err)
	}
}

func TestErrorsTravel(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
		msg  string
	}{
		{"typed", Errorf(http.StatusConflict, "this agent is a node; run this on the controller"), 409, "this agent is a node; run this on the controller"},
		{"plain", errors.New("disk on fire"), 500, "disk on fire"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewClient(startBackend(t, &fakeBackend{err: tt.err}))
			_, err := c.Discover(context.Background())
			var e *Error
			if !errors.As(err, &e) || e.Code != tt.code || e.Msg != tt.msg {
				t.Errorf("err = %#v", err)
			}
		})
	}
}

func TestBadBodyIs400(t *testing.T) {
	c := NewClient(startBackend(t, &fakeBackend{}))
	resp, err := c.hc.Post("http://sevjala/pair", "application/json", strings.NewReader("{"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status %d", resp.StatusCode)
	}
}

func (f *fakeBackend) DetectDatabase(context.Context) (Detection, error) {
	return Detection{Dumper: "/x/mysqldump", Databases: []DBDatabase{{Name: "shop", Configured: true}}}, f.err
}

func (f *fakeBackend) SetDatabase(_ context.Context, s DBSettings) (Detection, error) {
	f.mu.Lock()
	f.gotSettings = s
	f.mu.Unlock()
	return Detection{Dumper: "/x/mysqldump"}, f.err
}

func (f *fakeBackend) BackupNow(_ context.Context, db string, tags []string) ([]BackupJob, error) {
	f.mu.Lock()
	f.gotDB, f.gotTags = db, tags
	f.mu.Unlock()
	return []BackupJob{{BackupID: "BK-20261013-101500-A8F2", Database: "shop"}}, f.err
}

func (f *fakeBackend) Backups(context.Context) ([]Backup, error) { return nil, f.err }

func (f *fakeBackend) Backup(_ context.Context, id string) (Backup, error) {
	return Backup{BackupID: id, State: BackupDumping, Bytes: 7}, f.err
}

func TestBackupRoutesRoundTrip(t *testing.T) {
	f := &fakeBackend{}
	c := NewClient(startBackend(t, f))
	ctx := context.Background()
	if d, err := c.DetectDatabase(ctx); err != nil || d.Dumper != "/x/mysqldump" || !d.Databases[0].Configured {
		t.Errorf("DetectDatabase %+v, %v", d, err)
	}
	pw, port := "s3cret", 3307
	if _, err := c.SetDatabase(ctx, DBSettings{Password: &pw, Port: &port, Databases: []string{}}); err != nil {
		t.Fatal(err)
	}
	if s := f.gotSettings; s.Password == nil || *s.Password != pw || *s.Port != port || s.Host != nil || s.Databases == nil {
		t.Errorf("settings %+v", s)
	}
	if _, err := c.SetDatabase(ctx, DBSettings{}); err != nil || f.gotSettings.Databases != nil {
		t.Errorf("nil databases must stay nil: %+v, %v", f.gotSettings, err)
	}
	jobs, err := c.BackupNow(ctx, "shop", []string{"daily"})
	if err != nil || len(jobs) != 1 || f.gotDB != "shop" || !slices.Equal(f.gotTags, []string{"daily"}) {
		t.Errorf("BackupNow %+v, %v, %q %q", jobs, err, f.gotDB, f.gotTags)
	}
	if bs, err := c.Backups(ctx); err != nil || bs == nil || len(bs) != 0 {
		t.Errorf("Backups %+v, %v", bs, err)
	}
	if b, err := c.Backup(ctx, "BK-20261013-101500-A8F2"); err != nil || b.State != BackupDumping || b.Bytes != 7 {
		t.Errorf("Backup %+v, %v", b, err)
	}
}

func (f *fakeBackend) Policies(context.Context) ([]Policy, error) { return nil, f.err }

func (f *fakeBackend) AddPolicy(_ context.Context, ch PolicyChange) (Policy, error) {
	f.mu.Lock()
	f.gotPolicy = ch
	f.mu.Unlock()
	return Policy{Name: ch.Name, Keep: *ch.Keep, To: []PolicyTarget{}}, f.err
}

func (f *fakeBackend) SetPolicy(_ context.Context, name string, ch PolicyChange) (Policy, error) {
	f.mu.Lock()
	f.gotName, f.gotPolicy = name, ch
	f.mu.Unlock()
	return Policy{Name: name}, f.err
}

func (f *fakeBackend) RemovePolicy(_ context.Context, name string) error {
	f.mu.Lock()
	f.gotName = name
	f.mu.Unlock()
	return f.err
}

func TestPolicyRoutesRoundTrip(t *testing.T) {
	f := &fakeBackend{}
	c := NewClient(startBackend(t, f))
	ctx := context.Background()
	if ps, err := c.Policies(ctx); err != nil || ps == nil || len(ps) != 0 {
		t.Errorf("Policies %+v, %v", ps, err)
	}
	keep, sched := 4, "@weekly"
	if p, err := c.AddPolicy(ctx, PolicyChange{Name: "weekly", Keep: &keep, Schedule: &sched}); err != nil || p.Keep != 4 ||
		f.gotPolicy.To != nil || *f.gotPolicy.Schedule != sched {
		t.Errorf("AddPolicy %+v, %v, %+v", p, err, f.gotPolicy)
	}
	off := false
	if _, err := c.SetPolicy(ctx, "a b", PolicyChange{Enabled: &off, To: []string{}}); err != nil || f.gotName != "a b" ||
		f.gotPolicy.To == nil || *f.gotPolicy.Enabled || f.gotPolicy.Keep != nil {
		t.Errorf("SetPolicy %v, %q %+v", err, f.gotName, f.gotPolicy)
	}
	if err := c.RemovePolicy(ctx, "weekly"); err != nil || f.gotName != "weekly" {
		t.Errorf("RemovePolicy %v, %q", err, f.gotName)
	}
}
