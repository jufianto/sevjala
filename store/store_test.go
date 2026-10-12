package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

var paired = time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

func agent(id, host string) Agent {
	return Agent{AgentID: id, Hostname: host, LastIP: "10.0.0.2", Port: 17891, FP: "sha256:" + id,
		Role: "node", Version: "0.2.0", PairedAt: paired, State: "online"}
}

func TestOpenTwiceKeepsData(t *testing.T) {
	s, path := open(t)
	if err := s.UpsertAgent(context.Background(), agent("a", "PC-1")); err != nil {
		t.Fatal(err)
	}
	s.Close()
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if got, err := again.Agent(context.Background(), "a"); err != nil || got.Hostname != "PC-1" {
		t.Errorf("after reopen: %+v %v", got, err)
	}
}

func TestUpsertListGet(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	for _, a := range []Agent{agent("b", "zeta"), agent("a", "alpha")} {
		if err := s.UpsertAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	changed := agent("b", "zeta")
	changed.Version = "0.3.0"
	s.UpsertAgent(ctx, changed)

	all, err := s.Agents(ctx)
	if err != nil || len(all) != 2 || all[0].Hostname != "alpha" || all[1].Version != "0.3.0" {
		t.Fatalf("agents %+v %v", all, err)
	}
	got, _ := s.Agent(ctx, "a")
	if !got.PairedAt.Equal(paired) || !got.LastSeen.IsZero() || got.FP != "sha256:a" || got.Port != 17891 {
		t.Errorf("agent a %+v", got)
	}
	if _, err := s.Agent(ctx, "zz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing agent: %v", err)
	}
}

func TestMarkSeenAndUpdateAddress(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	a := agent("a", "PC-1")
	a.State = "offline"
	s.UpsertAgent(ctx, a)
	seen := paired.Add(time.Hour)
	if err := s.MarkSeen(ctx, "a", "0.4.0", seen); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAddress(ctx, "a", "PC-1B", "10.0.0.9", 18000); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Agent(ctx, "a")
	if got.Version != "0.4.0" || !got.LastSeen.Equal(seen) || got.State != "online" ||
		got.Hostname != "PC-1B" || got.LastIP != "10.0.0.9" || got.Port != 18000 {
		t.Errorf("agent %+v", got)
	}
	if err := s.MarkSeen(ctx, "zz", "x", seen); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkSeen missing: %v", err)
	}
	if err := s.UpdateAddress(ctx, "zz", "h", "ip", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateAddress missing: %v", err)
	}
}

func TestNewerSchemaRefused(t *testing.T) {
	s, path := open(t)
	if _, err := s.db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("err = %v", err)
	}
}
