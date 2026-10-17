package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
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

func TestRemoveAgent(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC)
	for _, id := range []string{"n1", "n2"} {
		if err := s.UpsertAgent(ctx, agent(id, "PC-"+id)); err != nil {
			t.Fatal(err)
		}
	}
	// BK-1 only on n1; BK-2 on both; BK-3 on n1 but expired.
	for _, id := range []string{"BK-1", "BK-2", "BK-3"} {
		if err := s.InsertBackup(ctx, backup(id), []string{"n1", "n2"}); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkCopy(ctx, id, "n1", CopyVerified, "", at); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkCopy(ctx, "BK-2", "n2", CopyVerified, "", at); err != nil {
		t.Fatal(err)
	}
	if err := s.Expire(ctx, "BK-3"); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertPolicy(ctx, Policy{Name: "offsite", Cron: "0 2 * * *", Keep: 3, Destinations: []string{"n1", "n2"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}

	only, err := s.OnlyCopies(ctx, "n1")
	if err != nil || !reflect.DeepEqual(only, []string{"BK-1"}) {
		t.Fatalf("only copies %q %v, want BK-1", only, err)
	}
	if err := s.RemoveAgent(ctx, "n1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Agent(ctx, "n1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("agent still there: %v", err)
	}
	for _, id := range []string{"BK-1", "BK-2"} {
		b, err := s.Backup(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range b.Copies {
			if c.AgentID == "n1" {
				t.Errorf("%s keeps a copy on the removed node", id)
			}
		}
	}
	if p, _ := s.Policy(ctx, "offsite"); !reflect.DeepEqual(p.Destinations, []string{"n2"}) {
		t.Errorf("destinations %q, want n2 only", p.Destinations)
	}
	if err := s.RemoveAgent(ctx, "n1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second remove: %v", err)
	}
}

func TestSetManager(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	if err := s.UpsertAgent(ctx, agent("n1", "PC-1")); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Agent(ctx, "n1"); a.Manager {
		t.Fatal("a new agent is a manager")
	}
	if err := s.SetManager(ctx, "n1", true); err != nil {
		t.Fatal(err)
	}
	if as, _ := s.Agents(ctx); len(as) != 1 || !as[0].Manager {
		t.Errorf("after on: %+v", as)
	}
	if err := s.UpsertAgent(ctx, agent("n1", "PC-1b")); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Agent(ctx, "n1"); !a.Manager || a.Hostname != "PC-1b" {
		t.Errorf("an address update dropped the flag: %+v", a)
	}
	if err := s.SetManager(ctx, "n1", false); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Agent(ctx, "n1"); a.Manager {
		t.Error("still a manager after off")
	}
	if err := s.SetManager(ctx, "nope", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown agent: %v", err)
	}
}
