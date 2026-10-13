package store

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	BackupCreated     = "created"
	BackupDistributed = "distributed"
	BackupPartial     = "partial"

	CopyPending      = "pending"
	CopyTransferring = "transferring"
	CopyVerified     = "verified"
	CopyFailed       = "failed"
	CopyOffline      = "offline"
)

type Backup struct {
	BackupID    string
	Database    string
	CreatedAt   time.Time
	Size        int64
	SHA256      string
	Tags        []string
	StagingPath string
	State       string
	Copies      []Copy
}

type Copy struct {
	BackupID   string
	AgentID    string
	State      string
	Attempts   int
	LastError  string
	VerifiedAt time.Time
}

const backupCols = `backup_id, "database", created_at, size, sha256, tags, staging_path, state`

// InsertBackup records a staged backup with one pending copy per agent.
func (s *Store) InsertBackup(ctx context.Context, b Backup, agentIDs []string) error {
	if b.Tags == nil {
		b.Tags = []string{}
	}
	tags, err := json.Marshal(b.Tags)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO backups (`+backupCols+`) VALUES (?,?,?,?,?,?,?,?)`,
		b.BackupID, b.Database, fmtTime(b.CreatedAt), b.Size, b.SHA256, string(tags), b.StagingPath, BackupCreated); err != nil {
		return err
	}
	for _, id := range agentIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO backup_copies (backup_id, agent_id, state) VALUES (?,?,?)`,
			b.BackupID, id, CopyPending); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Backups(ctx context.Context) ([]Backup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+backupCols+` FROM backups ORDER BY backup_id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	copies, err := s.copies(ctx, `SELECT * FROM backup_copies`)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Copies = copies[out[i].BackupID]
	}
	return out, nil
}

func (s *Store) Backup(ctx context.Context, id string) (Backup, error) {
	b, err := scanBackup(s.db.QueryRowContext(ctx, `SELECT `+backupCols+` FROM backups WHERE backup_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Backup{}, fmt.Errorf("backup %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Backup{}, err
	}
	copies, err := s.copies(ctx, `SELECT * FROM backup_copies WHERE backup_id = ?`, id)
	b.Copies = copies[id]
	return b, err
}

// SetCopy records a copy's new state and recomputes its backup's state. The
// last error survives retries until the copy is verified.
func (s *Store) SetCopy(ctx context.Context, c Copy) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE backup_copies SET state = ?1, attempts = ?2,
		last_error = CASE WHEN ?1 = 'verified' THEN '' WHEN ?3 <> '' THEN ?3 ELSE last_error END,
		verified_at = ?4 WHERE backup_id = ?5 AND agent_id = ?6`,
		c.State, c.Attempts, c.LastError, fmtTime(c.VerifiedAt), c.BackupID, c.AgentID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("copy %s on %s: %w", c.BackupID, c.AgentID, ErrNotFound)
	}
	rows, err := tx.QueryContext(ctx, `SELECT state FROM backup_copies WHERE backup_id = ?`, c.BackupID)
	if err != nil {
		return err
	}
	var states []string
	for rows.Next() {
		var st string
		if err := rows.Scan(&st); err != nil {
			rows.Close()
			return err
		}
		states = append(states, st)
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx, `UPDATE backups SET state = ? WHERE backup_id = ?`, backupState(states), c.BackupID); err != nil {
		return err
	}
	return tx.Commit()
}

func backupState(copies []string) string {
	verified := 0
	for _, st := range copies {
		switch st {
		case CopyPending, CopyTransferring:
			return BackupCreated
		case CopyVerified:
			verified++
		}
	}
	if len(copies) == 0 {
		return BackupCreated
	}
	if verified == len(copies) {
		return BackupDistributed
	}
	return BackupPartial
}

// Unsettled lists the copies a restart interrupted.
func (s *Store) Unsettled(ctx context.Context) ([]Copy, error) {
	all, err := s.copies(ctx, `SELECT * FROM backup_copies WHERE state IN ('pending', 'transferring')`)
	if err != nil {
		return nil, err
	}
	var out []Copy
	for _, cs := range all {
		out = append(out, cs...)
	}
	slices.SortFunc(out, func(a, b Copy) int {
		return cmp.Or(strings.Compare(a.BackupID, b.BackupID), strings.Compare(a.AgentID, b.AgentID))
	})
	return out, nil
}

// copies runs a "SELECT * FROM backup_copies …" query, grouped by backup.
func (s *Store) copies(ctx context.Context, query string, args ...any) (map[string][]Copy, error) {
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY backup_id, agent_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Copy{}
	for rows.Next() {
		var c Copy
		var at string
		if err := rows.Scan(&c.BackupID, &c.AgentID, &c.State, &c.Attempts, &c.LastError, &at); err != nil {
			return nil, err
		}
		if c.VerifiedAt, err = parseTime(at); err != nil {
			return nil, err
		}
		out[c.BackupID] = append(out[c.BackupID], c)
	}
	return out, rows.Err()
}

func scanBackup(r scanner) (Backup, error) {
	var b Backup
	var created, tags string
	if err := r.Scan(&b.BackupID, &b.Database, &created, &b.Size, &b.SHA256, &tags, &b.StagingPath, &b.State); err != nil {
		return Backup{}, err
	}
	var err error
	if b.CreatedAt, err = parseTime(created); err != nil {
		return Backup{}, err
	}
	return b, json.Unmarshal([]byte(tags), &b.Tags)
}
