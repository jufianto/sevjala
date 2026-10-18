package identity

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

var (
	uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	fpRe   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

func TestCreateThenLoad(t *testing.T) {
	dir := t.TempDir()
	id, created, err := LoadOrCreate(dir)
	if err != nil || !created {
		t.Fatalf("first LoadOrCreate: created=%v err=%v", created, err)
	}
	if !uuidRe.MatchString(id.AgentID) {
		t.Errorf("agent_id %q is not a UUIDv4", id.AgentID)
	}
	if !fpRe.MatchString(id.FP) {
		t.Errorf("fp %q", id.FP)
	}
	if cn := id.Cert.Leaf.Subject.CommonName; cn != id.AgentID {
		t.Errorf("CN %q != agent_id %q", cn, id.AgentID)
	}
	if id.Cert.Leaf.NotAfter.Before(time.Now().Add(9 * 365 * 24 * time.Hour)) {
		t.Errorf("cert expires %v, want ~10 years", id.Cert.Leaf.NotAfter)
	}
	again, created, err := LoadOrCreate(dir)
	if err != nil || created {
		t.Fatalf("second LoadOrCreate: created=%v err=%v", created, err)
	}
	if again.AgentID != id.AgentID || again.FP != id.FP {
		t.Errorf("reload changed identity: %s %s -> %s %s", id.AgentID, id.FP, again.AgentID, again.FP)
	}
}

func TestKeyIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows relies on the data dir ACL")
	}
	dir := t.TempDir()
	if _, _, err := LoadOrCreate(dir); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]os.FileMode{"key.pem": 0o600, "": 0o700} {
		fi, err := os.Stat(filepath.Join(Dir(dir), name))
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%q mode %v, want %v", name, got, want)
		}
	}
}

func TestMissingAgentIDRegenerates(t *testing.T) {
	dir := t.TempDir()
	first, _, _ := LoadOrCreate(dir)
	os.Remove(filepath.Join(Dir(dir), "agent_id"))
	second, created, err := LoadOrCreate(dir)
	if err != nil || !created || second.AgentID == first.AgentID {
		t.Fatalf("created=%v err=%v same id=%v", created, err, second != nil && second.AgentID == first.AgentID)
	}
}

func TestBrokenIdentityIsAnError(t *testing.T) {
	tests := []struct {
		name   string
		breakf func(dir string)
		want   string
	}{
		{"missing cert", func(dir string) { os.Remove(filepath.Join(Dir(dir), "cert.pem")) }, "unusable"},
		{"other agent_id", func(dir string) {
			os.WriteFile(filepath.Join(Dir(dir), "agent_id"), []byte("00000000-0000-4000-8000-000000000000\n"), 0o644)
		}, "does not match"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			LoadOrCreate(dir)
			tt.breakf(dir)
			_, _, err := LoadOrCreate(dir)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
			if errors.Is(err, fs.ErrNotExist) {
				t.Error("a broken identity must not look like a fresh install")
			}
		})
	}
}

func TestControllerPeerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if p, err := LoadController(dir); p != nil || err != nil {
		t.Fatalf("unpaired: %+v %v", p, err)
	}
	want := Peer{AgentID: "c1", FP: "sha256:ab", PairedAt: time.Now().UTC().Truncate(time.Second)}
	if err := SaveController(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadController(dir)
	if err != nil || got == nil || got.AgentID != want.AgentID || got.FP != want.FP || !got.PairedAt.Equal(want.PairedAt) {
		t.Fatalf("got %+v %v, want %+v", got, err, want)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(Dir(dir), "peers.json"))
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("peers.json mode %v", fi.Mode().Perm())
		}
	}
}

func TestForgetController(t *testing.T) {
	dir := t.TempDir()
	if err := SaveController(dir, Peer{AgentID: "c1", FP: "sha256:ab"}); err != nil {
		t.Fatal(err)
	}
	if err := ForgetController(dir); err != nil {
		t.Fatal(err)
	}
	if p, err := LoadController(dir); p != nil || err != nil {
		t.Errorf("after forget: %+v %v", p, err)
	}
	if err := ForgetController(dir); err != nil {
		t.Errorf("forgetting twice: %v", err)
	}
}
