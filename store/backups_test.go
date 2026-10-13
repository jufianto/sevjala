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

func backup(id string) Backup {
	return Backup{BackupID: id, Database: "shop", CreatedAt: time.Date(2026, 10, 13, 10, 15, 0, 0, time.UTC),
		Size: 42, SHA256: "ab", Tags: []string{"manual"}, StagingPath: id + ".sql.gz"}
}

func TestMigrateV1ToV2KeepsAgents(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 1; INSERT INTO agents (agent_id, hostname, last_ip, port, fp, role, version, paired_at)
		VALUES ('n1', 'PC', '10.0.0.2', 17891, 'sha256:x', 'node', '0.1.0', '2026-10-12T09:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v int
	_ = s.db.QueryRow("PRAGMA user_version").Scan(&v)
	if v != len(migrations) {
		t.Errorf("user_version %d", v)
	}
	if _, err := s.Agent(context.Background(), "n1"); err != nil {
		t.Errorf("agent lost: %v", err)
	}
}

func TestInsertAndListBackups(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	if err := s.InsertBackup(ctx, backup("BK-20261013-100000-0001"), []string{"n1", "n2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertBackup(ctx, backup("BK-20261013-110000-0002"), nil); err != nil {
		t.Fatal(err)
	}
	bs, err := s.Backups(ctx, true)
	if err != nil || len(bs) != 2 || bs[0].BackupID != "BK-20261013-110000-0002" {
		t.Fatalf("backups %+v, %v", bs, err)
	}
	b := bs[1]
	want := backup("BK-20261013-100000-0001")
	want.State = BackupCreated
	want.Copies = []Copy{{BackupID: want.BackupID, AgentID: "n1", State: CopyPending}, {BackupID: want.BackupID, AgentID: "n2", State: CopyPending}}
	if !reflect.DeepEqual(b, want) {
		t.Errorf("got  %+v\nwant %+v", b, want)
	}
	if one, err := s.Backup(ctx, want.BackupID); err != nil || !reflect.DeepEqual(one, want) {
		t.Errorf("Backup = %+v, %v", one, err)
	}
	if _, err := s.Backup(ctx, "BK-20261013-120000-0003"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
}

func TestSetCopyRecomputesBackupState(t *testing.T) {
	tests := []struct {
		name   string
		states []string
		want   string
	}{
		{"all verified", []string{CopyVerified, CopyVerified}, BackupDistributed},
		{"one failed", []string{CopyVerified, CopyFailed}, BackupPartial},
		{"one offline", []string{CopyOffline, CopyVerified}, BackupPartial},
		{"one still moving", []string{CopyVerified, CopyTransferring}, BackupCreated},
		{"one waiting to retry", []string{CopyFailed, CopyPending}, BackupCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s, _ := open(t)
			id := "BK-20261013-100000-0001"
			_ = s.InsertBackup(ctx, backup(id), []string{"n1", "n2"})
			for i, st := range tt.states {
				if err := s.SetCopy(ctx, Copy{BackupID: id, AgentID: []string{"n1", "n2"}[i], State: st, Attempts: 1}); err != nil {
					t.Fatal(err)
				}
			}
			if b, _ := s.Backup(ctx, id); b.State != tt.want {
				t.Errorf("state %s, want %s", b.State, tt.want)
			}
		})
	}
}

func TestSetCopyKeepsLastErrorUntilVerified(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	id := "BK-20261013-100000-0001"
	_ = s.InsertBackup(ctx, backup(id), []string{"n1"})
	_ = s.SetCopy(ctx, Copy{BackupID: id, AgentID: "n1", State: CopyPending, Attempts: 1, LastError: "connection refused"})
	_ = s.SetCopy(ctx, Copy{BackupID: id, AgentID: "n1", State: CopyTransferring, Attempts: 2})
	if b, _ := s.Backup(ctx, id); b.Copies[0].LastError != "connection refused" || b.Copies[0].Attempts != 2 {
		t.Errorf("copy %+v", b.Copies[0])
	}
	at := time.Date(2026, 10, 13, 10, 20, 0, 0, time.UTC)
	_ = s.SetCopy(ctx, Copy{BackupID: id, AgentID: "n1", State: CopyVerified, Attempts: 2, VerifiedAt: at})
	if b, _ := s.Backup(ctx, id); b.Copies[0].LastError != "" || !b.Copies[0].VerifiedAt.Equal(at) {
		t.Errorf("copy %+v", b.Copies[0])
	}
	if err := s.SetCopy(ctx, Copy{BackupID: id, AgentID: "nope", State: CopyFailed}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown copy: %v", err)
	}
}

func TestUnsettled(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	id := "BK-20261013-100000-0001"
	_ = s.InsertBackup(ctx, backup(id), []string{"n1", "n2", "n3"})
	_ = s.SetCopy(ctx, Copy{BackupID: id, AgentID: "n1", State: CopyVerified})
	_ = s.SetCopy(ctx, Copy{BackupID: id, AgentID: "n2", State: CopyTransferring})
	cs, err := s.Unsettled(ctx)
	if err != nil || len(cs) != 2 || cs[0].AgentID != "n2" || cs[1].AgentID != "n3" {
		t.Errorf("unsettled %+v, %v", cs, err)
	}
}
