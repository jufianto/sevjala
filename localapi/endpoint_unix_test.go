//go:build !windows

package localapi

import (
	"context"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/jufianto/sevjala/config"
)

func TestStaleSocketRemoved(t *testing.T) {
	ep := Endpoint(testDir(t))
	ln, err := net.Listen("unix", ep)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()

	srv, err := Start(ep, NewHandler(&fakeBackend{}), discard)
	if err != nil {
		t.Fatalf("stale socket blocked Start: %v", err)
	}
	srv.Shutdown(context.Background())
	if _, err := os.Stat(ep); !os.IsNotExist(err) {
		t.Error("socket left behind after Shutdown")
	}
}

func TestSocketPermissions(t *testing.T) {
	ep := startTest(t, Status{})
	info, err := os.Stat(ep)
	if err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0o600)
	if _, err := user.LookupGroup(config.SocketGroup); err == nil && os.Geteuid() == 0 {
		want = 0o660
	}
	if info.Mode().Perm() != want {
		t.Errorf("mode = %v, want %v", info.Mode().Perm(), want)
	}
}

func TestStartErrorsOnBadEndpoint(t *testing.T) {
	ep := filepath.Join(testDir(t), "missing-dir", "sevjala.sock")
	if _, err := Start(ep, NewHandler(&fakeBackend{}), discard); err == nil {
		t.Fatal("Start succeeded in a missing directory")
	}
}
