package uiproxy

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jufianto/sevjala/localapi"
)

var assets = fstest.MapFS{
	"index.html":    {Data: []byte("<!doctype html><title>Sevjala</title>dashboard-index")},
	"assets/app.js": {Data: []byte("console.log('app')")},
}

// fakeAgent answers on a real local endpoint, as the service would.
type fakeAgent struct {
	mu      sync.Mutex
	status  localapi.Status
	agents  []localapi.Agent
	cookies []string
	posts   int
}

func (f *fakeAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cookies = append(f.cookies, r.Header.Get("Cookie"))
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/status":
		_ = json.NewEncoder(w).Encode(f.status)
	case r.Method == http.MethodGet && r.URL.Path == "/agents":
		_ = json.NewEncoder(w).Encode(map[string]any{"agents": f.agents})
	case r.Method == http.MethodPost && r.URL.Path == "/backup/now":
		f.posts++
		_, _ = io.WriteString(w, `{"backups":[]}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"not found"}`)
	}
}

func (f *fakeAgent) set(st localapi.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = st
}

func shortDir(t *testing.T) string {
	t.Helper()
	base := os.TempDir()
	if runtime.GOOS != "windows" {
		base = "/tmp" // a Unix socket path must stay short
	}
	dir, err := os.MkdirTemp(base, "sj")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func startAgent(t *testing.T, h http.Handler) string {
	t.Helper()
	ep := localapi.Endpoint(shortDir(t))
	srv, err := localapi.Start(ep, h, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown(context.Background()) })
	return ep
}

// start serves a dashboard for the agent at endpoint and returns it with a
// client that keeps cookies but does not follow redirects.
func start(t *testing.T, endpoint string, files fstest.MapFS, idle time.Duration) (*Server, *http.Client, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(endpoint, files, ln)
	if err != nil {
		t.Fatal(err)
	}
	s.Poll = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, idle) }()
	t.Cleanup(cancel)
	jar, _ := cookiejar.New(nil)
	cl := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return s, cl, done
}

func get(t *testing.T, cl *http.Client, url string) (*http.Response, string) {
	t.Helper()
	resp, err := cl.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func login(t *testing.T, s *Server, cl *http.Client) {
	t.Helper()
	resp, _ := get(t, cl, s.URL())
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("token: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestTheLinkOpensASessionOnce(t *testing.T) {
	s, cl, _ := start(t, startAgent(t, &fakeAgent{}), assets, time.Minute)
	resp, _ := get(t, cl, s.URL())
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("token: %d", resp.StatusCode)
	}
	var c *http.Cookie
	for _, k := range resp.Cookies() {
		if strings.HasPrefix(k.Name, "sevjala_session") {
			c = k
		}
	}
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie %+v", c)
	}
	resp, body := get(t, cl, s.Base()+"/")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "dashboard-index") {
		t.Errorf("index: %d %q", resp.StatusCode, body)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'self'") ||
		resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Errorf("headers %v", resp.Header)
	}

	other := &http.Client{CheckRedirect: cl.CheckRedirect}
	resp, body = get(t, other, s.URL())
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "sevjala ui") {
		t.Errorf("the link used twice: %d %q", resp.StatusCode, body)
	}
}

func TestWithoutASessionNothingIsServed(t *testing.T) {
	s, _, _ := start(t, startAgent(t, &fakeAgent{}), assets, time.Minute)
	cl := &http.Client{Timeout: 2 * time.Second}
	for _, p := range []string{"/", "/assets/app.js", "/api/status", "/events", "/?token=wrong"} {
		resp, body := get(t, cl, s.Base()+p)
		if resp.StatusCode < 400 || strings.Contains(body, "dashboard-index") {
			t.Errorf("%s: %d %q", p, resp.StatusCode, body)
		}
	}
}

func TestAnotherHostNameIsRefused(t *testing.T) {
	s, cl, _ := start(t, startAgent(t, &fakeAgent{}), assets, time.Minute)
	login(t, s, cl)
	req, _ := http.NewRequest(http.MethodGet, s.Base()+"/api/status", nil)
	req.Host = "evil.example" + s.Base()[strings.LastIndex(s.Base(), ":"):]
	resp, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("rebinding host: %d", resp.StatusCode)
	}
}

func TestTheAPIGoesToTheAgentWithoutTheCookie(t *testing.T) {
	f := &fakeAgent{status: localapi.Status{Version: "0.4.0", Role: "controller", Hostname: "SERVER-01"}}
	s, cl, _ := start(t, startAgent(t, f), assets, time.Minute)
	login(t, s, cl)
	resp, body := get(t, cl, s.Base()+"/api/status")
	var st localapi.Status
	if resp.StatusCode != http.StatusOK || json.Unmarshal([]byte(body), &st) != nil || st.Hostname != "SERVER-01" {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.cookies {
		if c != "" {
			t.Errorf("the agent saw cookie %q", c)
		}
	}
}

func TestChangesNeedTheDashboardHeader(t *testing.T) {
	f := &fakeAgent{}
	s, cl, _ := start(t, startAgent(t, f), assets, time.Minute)
	login(t, s, cl)
	post := func(header bool) int {
		req, _ := http.NewRequest(http.MethodPost, s.Base()+"/api/backup/now", strings.NewReader(`{}`))
		if header {
			req.Header.Set(changeHeader, "1")
		}
		resp, err := cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(false); code != http.StatusForbidden {
		t.Errorf("without the header: %d", code)
	}
	if code := post(true); code != http.StatusOK {
		t.Errorf("with the header: %d", code)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.posts != 1 {
		t.Errorf("the agent got %d posts, want 1", f.posts)
	}
}

func TestAStoppedAgentIsNamed(t *testing.T) {
	s, cl, _ := start(t, localapi.Endpoint(shortDir(t)), assets, time.Minute)
	login(t, s, cl)
	resp, body := get(t, cl, s.Base()+"/api/status")
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, "service is not running") {
		t.Errorf("%d %q", resp.StatusCode, body)
	}
}

func TestADashboardlessBuildSaysSo(t *testing.T) {
	s, cl, _ := start(t, startAgent(t, &fakeAgent{}), fstest.MapFS{".gitkeep": {}}, time.Minute)
	login(t, s, cl)
	resp, body := get(t, cl, s.Base()+"/")
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "built without the dashboard") {
		t.Errorf("%d %q", resp.StatusCode, body)
	}
}

type event struct{ name, data string }

func readEvent(t *testing.T, r *bufio.Reader) event {
	t.Helper()
	var e event
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("events: %v", err)
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			e.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			e.data = strings.TrimPrefix(line, "data: ")
		case line == "" && e.name != "":
			return e
		}
	}
}

func TestEventsFollowTheAgent(t *testing.T) {
	f := &fakeAgent{status: localapi.Status{Role: "controller", Health: &localapi.Health{Level: "ok", Headline: "Protected"}},
		agents: []localapi.Agent{{Hostname: "PC-1", State: "online"}}}
	ep := startAgent(t, f)
	s, cl, _ := start(t, ep, assets, time.Minute)
	login(t, s, cl)
	resp, err := cl.Get(s.Base() + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	r := bufio.NewReader(resp.Body)
	e := readEvent(t, r)
	var st State
	if e.name != "state" || json.Unmarshal([]byte(e.data), &st) != nil || st.Status.Health.Headline != "Protected" ||
		len(st.Agents) != 1 || st.Agents[0].Hostname != "PC-1" {
		t.Fatalf("first event %+v", e)
	}
	f.set(localapi.Status{Role: "controller", Health: &localapi.Health{Level: "problem", Headline: "Disk almost full"}})
	if e = readEvent(t, r); !strings.Contains(e.data, "Disk almost full") {
		t.Errorf("after a change %+v", e)
	}
}

func TestEventsSayWhenTheAgentStops(t *testing.T) {
	s, cl, _ := start(t, localapi.Endpoint(shortDir(t)), assets, time.Minute)
	login(t, s, cl)
	resp, err := cl.Get(s.Base() + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if e := readEvent(t, bufio.NewReader(resp.Body)); e.name != "down" || !strings.Contains(e.data, "service is not running") {
		t.Errorf("%+v", e)
	}
}

func TestItStopsOnceTheTabIsClosed(t *testing.T) {
	s, cl, done := start(t, startAgent(t, &fakeAgent{}), assets, 300*time.Millisecond)
	login(t, s, cl)
	resp, err := cl.Get(s.Base() + "/events")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		t.Fatalf("stopped with a tab open: %v", err)
	case <-time.After(time.Second):
	}
	resp.Body.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("stopped with %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("still running after the tab closed")
	}
}

// Cookies are kept per host, not per port: a second dashboard must not log
// the first tab out.
func TestTwoDashboardsKeepTheirOwnSessions(t *testing.T) {
	ep := startAgent(t, &fakeAgent{})
	a, cl, _ := start(t, ep, assets, time.Minute)
	b, _, _ := start(t, ep, assets, time.Minute)
	login(t, a, cl)
	login(t, b, cl)
	for _, s := range []*Server{a, b} {
		if resp, _ := get(t, cl, s.Base()+"/api/status"); resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %d", s.Base(), resp.StatusCode)
		}
	}
}
