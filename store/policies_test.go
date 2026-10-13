package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestMigrateV2ToV3SeedsPolicies(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:2] {
		if _, err := db.Exec(m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version = 2; INSERT INTO backups VALUES
		('BK-20261013-100000-0001', 'shop', '2026-10-13T10:00:00Z', 42, 'ab', '["manual"]', 'x.sql.gz', 'distributed')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	ps, err := s.Policies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []Policy{
		{Name: "frequent", Cron: "*/10 * * * *", Keep: 12, Destinations: []string{}, Enabled: true},
		{Name: "hourly", Cron: "0 * * * *", Keep: 24, Destinations: []string{}, Enabled: true},
		{Name: "daily", Cron: "0 1 * * *", Keep: 30, Destinations: []string{}, Enabled: true},
		{Name: "monthly", Cron: "0 1 1 * *", Keep: 12, Destinations: []string{}, Enabled: true},
		{Name: "manual", Cron: "", Keep: 10, Destinations: []string{}, Enabled: true},
	}
	if !reflect.DeepEqual(ps, want) {
		t.Errorf("policies\n got %+v\nwant %+v", ps, want)
	}
	if bs, _ := s.Backups(ctx, true); len(bs) != 1 {
		t.Errorf("backups lost: %+v", bs)
	}
}

func TestPolicyCRUD(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	p := Policy{Name: "weekly", Cron: "@weekly", Keep: 4, Destinations: []string{"n1"}, Enabled: true}
	if err := s.InsertPolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertPolicy(ctx, p); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate: %v", err)
	}
	if got, err := s.Policy(ctx, "weekly"); err != nil || !reflect.DeepEqual(got, p) {
		t.Errorf("Policy = %+v, %v", got, err)
	}
	p.Keep, p.Enabled, p.Destinations = 8, false, []string{}
	if err := s.UpdatePolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Policy(ctx, "weekly"); !reflect.DeepEqual(got, p) {
		t.Errorf("after update %+v", got)
	}
	if ps, _ := s.Policies(ctx); ps[len(ps)-1].Name != "weekly" {
		t.Errorf("new policy not listed last: %+v", ps)
	}
	if err := s.DeletePolicy(ctx, "weekly"); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{s.DeletePolicy(ctx, "weekly"), s.UpdatePolicy(ctx, p)} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("missing policy: %v", err)
		}
	}
	if _, err := s.Policy(ctx, "weekly"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Policy after delete: %v", err)
	}
}

func TestEnsureCopy(t *testing.T) {
	tests := []struct{ from, want string }{
		{"", CopyPending},
		{CopyFailed, CopyPending},
		{CopyOffline, CopyPending},
		{CopyCorrupt, CopyPending},
		{CopyDeleted, CopyPending},
		{CopyTransferring, CopyTransferring},
		{CopyVerified, CopyVerified},
	}
	for _, tt := range tests {
		t.Run("from "+tt.from, func(t *testing.T) {
			ctx := context.Background()
			s, _ := open(t)
			id := "BK-20261013-100000-0001"
			var agents []string
			if tt.from != "" {
				agents = []string{"n1"}
			}
			_ = s.InsertBackup(ctx, backup(id), agents)
			if tt.from != "" {
				_ = s.SetCopy(ctx, Copy{BackupID: id, AgentID: "n1", State: tt.from, Attempts: 3})
			}
			if err := s.EnsureCopy(ctx, id, "n1"); err != nil {
				t.Fatal(err)
			}
			b, _ := s.Backup(ctx, id)
			if len(b.Copies) != 1 || b.Copies[0].State != tt.want {
				t.Errorf("copies %+v", b.Copies)
			}
		})
	}
}

func TestBackupStateIgnoresDeletedCopies(t *testing.T) {
	tests := []struct {
		name   string
		states []string
		want   string
	}{
		{"verified and deleted", []string{CopyVerified, CopyDeleted}, BackupDistributed},
		{"corrupt and deleted", []string{CopyCorrupt, CopyDeleted}, BackupPartial},
		{"all deleted", []string{CopyDeleted, CopyDeleted}, BackupCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s, _ := open(t)
			id := "BK-20261013-100000-0001"
			_ = s.InsertBackup(ctx, backup(id), []string{"n1", "n2"})
			for i, st := range tt.states {
				if err := s.SetCopy(ctx, Copy{BackupID: id, AgentID: []string{"n1", "n2"}[i], State: st}); err != nil {
					t.Fatal(err)
				}
			}
			if b, _ := s.Backup(ctx, id); b.State != tt.want {
				t.Errorf("state %s, want %s", b.State, tt.want)
			}
		})
	}
}

func TestMarkCopyKeepsAttempts(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	id := "BK-20261013-100000-0001"
	_ = s.InsertBackup(ctx, backup(id), []string{"n1"})
	_ = s.SetCopy(ctx, Copy{BackupID: id, AgentID: "n1", State: CopyFailed, Attempts: 4, LastError: "refused"})
	at := time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC)
	if err := s.MarkCopy(ctx, id, "n1", CopyVerified, "", at); err != nil {
		t.Fatal(err)
	}
	b, _ := s.Backup(ctx, id)
	if c := b.Copies[0]; c.State != CopyVerified || c.Attempts != 4 || c.LastError != "" || !c.VerifiedAt.Equal(at) || b.State != BackupDistributed {
		t.Errorf("backup %+v", b)
	}
	if err := s.MarkCopy(ctx, id, "n2", CopyCorrupt, "size differs", time.Time{}); err != nil {
		t.Fatal(err)
	}
	b, _ = s.Backup(ctx, id)
	if len(b.Copies) != 2 || b.Copies[1].State != CopyCorrupt || b.Copies[1].LastError != "size differs" || b.State != BackupPartial {
		t.Errorf("new copy %+v", b)
	}
	_ = s.MarkCopy(ctx, id, "n1", CopyFailed, "missing from the node", time.Time{})
	if b, _ := s.Backup(ctx, id); !b.Copies[0].VerifiedAt.Equal(at) || b.Copies[0].LastError != "missing from the node" {
		t.Errorf("failed copy %+v", b.Copies[0])
	}
}

func TestUnstageAndExpire(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	old, kept := "BK-20261013-100000-0001", "BK-20261013-110000-0002"
	_ = s.InsertBackup(ctx, backup(old), []string{"n1"})
	_ = s.InsertBackup(ctx, backup(kept), nil)
	if err := s.Unstage(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := s.Expire(ctx, old); err != nil {
		t.Fatal(err)
	}
	_ = s.SetCopy(ctx, Copy{BackupID: old, AgentID: "n1", State: CopyDeleted})
	b, _ := s.Backup(ctx, old)
	if b.StagingPath != "" || b.State != BackupExpired {
		t.Errorf("expired backup %+v", b)
	}
	live, _ := s.Backups(ctx, false)
	all, _ := s.Backups(ctx, true)
	if len(live) != 1 || live[0].BackupID != kept || len(all) != 2 {
		t.Errorf("live %d, all %d", len(live), len(all))
	}
	for _, err := range []error{s.Unstage(ctx, "BK-20261013-120000-0003"), s.Expire(ctx, "BK-20261013-120000-0003")} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown backup: %v", err)
		}
	}
}

func TestRecordPollAndState(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	_ = s.UpsertAgent(ctx, agent("n1", "PC-1"))
	at := time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC)
	if err := s.RecordPoll(ctx, "n1", AgentWarning, "0.3.0", 5<<30, at); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Agent(ctx, "n1")
	if a.State != AgentWarning || a.Version != "0.3.0" || a.FreeBytes != 5<<30 || !a.LastSeen.Equal(at) {
		t.Errorf("after poll %+v", a)
	}
	if err := s.SetAgentState(ctx, "n1", AgentOffline); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Agent(ctx, "n1")
	if a.State != AgentOffline || a.FreeBytes != 5<<30 || !a.LastSeen.Equal(at) {
		t.Errorf("after offline %+v", a)
	}
	for _, err := range []error{s.SetAgentState(ctx, "nope", AgentOffline), s.RecordPoll(ctx, "nope", AgentOnline, "", 0, at)} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown agent: %v", err)
		}
	}
}

func TestVerifiedCopyRevivesAnExpiredBackup(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	id := "BK-20261013-100000-0001"
	_ = s.InsertBackup(ctx, backup(id), []string{"n1"})
	_ = s.Unstage(ctx, id)
	_ = s.Expire(ctx, id)
	if err := s.MarkCopy(ctx, id, "n1", CopyFailed, "missing from the node", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.Backup(ctx, id); b.State != BackupExpired {
		t.Fatalf("failed copy changed the state to %s", b.State)
	}
	if err := s.MarkCopy(ctx, id, "n1", CopyVerified, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.Backup(ctx, id); b.State != BackupDistributed {
		t.Errorf("state %s, want distributed", b.State)
	}
}
