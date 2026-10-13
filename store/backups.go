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
	BackupExpired     = "expired" // set only by Expire; no copy and no staging file left

	CopyPending      = "pending"
	CopyTransferring = "transferring"
	CopyVerified     = "verified"
	CopyFailed       = "failed"
	CopyOffline      = "offline"
	CopyCorrupt      = "corrupt"
	CopyDeleted      = "deleted"
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

// Backups lists newest first; expired backups only when withExpired.
func (s *Store) Backups(ctx context.Context, withExpired bool) ([]Backup, error) {
	where := ` WHERE state <> 'expired'`
	if withExpired {
		where = ""
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+backupCols+` FROM backups`+where+` ORDER BY backup_id DESC`)
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
	if err := recompute(ctx, tx, c.BackupID); err != nil {
		return err
	}
	return tx.Commit()
}

// EnsureCopy plans a copy for a push: a new pending row, or a failed, offline,
// corrupt or deleted one back to pending. A copy in flight or verified stays.
func (s *Store) EnsureCopy(ctx context.Context, backupID, agentID string) error {
	return s.inTx(ctx, backupID, `INSERT INTO backup_copies (backup_id, agent_id, state) VALUES (?1, ?2, 'pending')
		ON CONFLICT(backup_id, agent_id) DO UPDATE SET state = 'pending'
		WHERE state IN ('failed', 'offline', 'corrupt', 'deleted')`, backupID, agentID)
}

// MarkCopy records what a node's inventory showed, keeping the attempt count:
// verified sets verified_at to at and clears the error; another state keeps
// verified_at and stores reason when given.
func (s *Store) MarkCopy(ctx context.Context, backupID, agentID, state, reason string, at time.Time) error {
	return s.inTx(ctx, backupID, `INSERT INTO backup_copies (backup_id, agent_id, state, last_error, verified_at)
		VALUES (?1, ?2, ?3, ?4, CASE WHEN ?3 = 'verified' THEN ?5 ELSE '' END)
		ON CONFLICT(backup_id, agent_id) DO UPDATE SET state = ?3,
		last_error = CASE WHEN ?3 = 'verified' THEN '' WHEN ?4 <> '' THEN ?4 ELSE last_error END,
		verified_at = CASE WHEN ?3 = 'verified' THEN ?5 ELSE verified_at END`,
		backupID, agentID, state, reason, fmtTime(at))
}

// Unstage records that the staging file is gone.
func (s *Store) Unstage(ctx context.Context, id string) error {
	return s.inTx(ctx, id, `UPDATE backups SET staging_path = '' WHERE backup_id = ?`, id)
}

func (s *Store) Expire(ctx context.Context, id string) error {
	return s.affect(ctx, "backup "+id, `UPDATE backups SET state = 'expired' WHERE backup_id = ?`, id)
}

// inTx runs one statement and recomputes the backup's state in the same transaction.
func (s *Store) inTx(ctx context.Context, backupID, query string, args ...any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	if err := recompute(ctx, tx, backupID); err != nil {
		return err
	}
	return tx.Commit()
}

// recompute derives a backup's state from its copies; an expired backup stays expired.
func recompute(ctx context.Context, tx *sql.Tx, id string) error {
	var state string
	err := tx.QueryRowContext(ctx, `SELECT state FROM backups WHERE backup_id = ?`, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("backup %s: %w", id, ErrNotFound)
	}
	if err != nil || state == BackupExpired {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT state FROM backup_copies WHERE backup_id = ?`, id)
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
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE backups SET state = ? WHERE backup_id = ?`, backupState(states), id)
	return err
}

// backupState ignores deleted copies: retention removing a copy does not make
// a backup partial.
func backupState(copies []string) string {
	live, verified := 0, 0
	for _, st := range copies {
		switch st {
		case CopyPending, CopyTransferring:
			return BackupCreated
		case CopyDeleted:
			continue
		case CopyVerified:
			verified++
		}
		live++
	}
	switch {
	case live == 0:
		return BackupCreated
	case verified == live:
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
