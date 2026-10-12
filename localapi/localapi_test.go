package localapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
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

func startTest(t *testing.T, st Status) string {
	t.Helper()
	ep := Endpoint(testDir(t))
	srv, err := Start(ep, NewHandler(func() Status { return st }), discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown(context.Background()) })
	return ep
}

func TestStatusRoundTrip(t *testing.T) {
	want := Status{Version: "0.1.0", Role: "node", PID: 42, UptimeS: 7, DataDir: "/x"}
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
	if srv, err := Start(ep, NewHandler(func() Status { return Status{} }), discard); err == nil {
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
