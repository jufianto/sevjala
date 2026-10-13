// Package store is the Controller's SQLite state (blueprint: Data model →
// Controller store). Only a controller-role agent opens it.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const FileName = "controller.db"

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
)

const (
	AgentOnline  = "online"
	AgentWarning = "warning"
	AgentOffline = "offline"
	AgentError   = "error"
)

// migrations[i] moves the schema from user_version i to i+1.
var migrations = []string{
	`CREATE TABLE agents (
		agent_id   TEXT PRIMARY KEY,
		hostname   TEXT NOT NULL,
		last_ip    TEXT NOT NULL,
		port       INTEGER NOT NULL,
		fp         TEXT NOT NULL,
		role       TEXT NOT NULL,
		version    TEXT NOT NULL,
		paired_at  TEXT NOT NULL,
		last_seen  TEXT NOT NULL DEFAULT '',
		state      TEXT NOT NULL DEFAULT 'online',
		free_bytes INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE backups (
		backup_id    TEXT PRIMARY KEY,
		"database"   TEXT NOT NULL,
		created_at   TEXT NOT NULL,
		size         INTEGER NOT NULL,
		sha256       TEXT NOT NULL,
		tags         TEXT NOT NULL,
		staging_path TEXT NOT NULL,
		state        TEXT NOT NULL
	);
	CREATE TABLE backup_copies (
		backup_id   TEXT NOT NULL REFERENCES backups(backup_id),
		agent_id    TEXT NOT NULL,
		state       TEXT NOT NULL,
		attempts    INTEGER NOT NULL DEFAULT 0,
		last_error  TEXT NOT NULL DEFAULT '',
		verified_at TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (backup_id, agent_id)
	)`,
	`CREATE TABLE policies (
		name         TEXT PRIMARY KEY,
		cron         TEXT NOT NULL,
		keep         INTEGER NOT NULL,
		destinations TEXT NOT NULL DEFAULT '[]',
		enabled      INTEGER NOT NULL DEFAULT 1
	);
	INSERT INTO policies (name, cron, keep) VALUES
		('frequent', '*/10 * * * *', 12), ('hourly', '0 * * * *', 24),
		('daily', '0 1 * * *', 30), ('monthly', '0 1 1 * *', 12), ('manual', '', 10)`,
}

type Agent struct {
	AgentID   string
	Hostname  string
	LastIP    string
	Port      int
	FP        string
	Role      string
	Version   string
	PairedAt  time.Time
	LastSeen  time.Time
	State     string
	FreeBytes int64
}

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v > len(migrations) {
		return fmt.Errorf("schema version %d is newer than this build (%d)", v, len(migrations))
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

const agentCols = `agent_id, hostname, last_ip, port, fp, role, version, paired_at, last_seen, state, free_bytes`

func (s *Store) UpsertAgent(ctx context.Context, a Agent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agents (`+agentCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(agent_id) DO UPDATE SET hostname=excluded.hostname, last_ip=excluded.last_ip,
		port=excluded.port, fp=excluded.fp, role=excluded.role, version=excluded.version,
		paired_at=excluded.paired_at, last_seen=excluded.last_seen, state=excluded.state,
		free_bytes=excluded.free_bytes`,
		a.AgentID, a.Hostname, a.LastIP, a.Port, a.FP, a.Role, a.Version,
		fmtTime(a.PairedAt), fmtTime(a.LastSeen), a.State, a.FreeBytes)
	return err
}

func (s *Store) Agents(ctx context.Context) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+agentCols+` FROM agents ORDER BY hostname, agent_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Agent(ctx context.Context, id string) (Agent, error) {
	a, err := scanAgent(s.db.QueryRowContext(ctx, `SELECT `+agentCols+` FROM agents WHERE agent_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, fmt.Errorf("agent %s: %w", id, ErrNotFound)
	}
	return a, err
}

func (s *Store) MarkSeen(ctx context.Context, id, version string, at time.Time) error {
	return s.update(ctx, id, `UPDATE agents SET last_seen = ?, version = ?, state = 'online' WHERE agent_id = ?`,
		fmtTime(at), version, id)
}

func (s *Store) UpdateAddress(ctx context.Context, id, hostname, ip string, port int) error {
	return s.update(ctx, id, `UPDATE agents SET hostname = ?, last_ip = ?, port = ? WHERE agent_id = ?`,
		hostname, ip, port, id)
}

func (s *Store) update(ctx context.Context, id, query string, args ...any) error {
	return s.affect(ctx, "agent "+id, query, args...)
}

type scanner interface{ Scan(dest ...any) error }

func scanAgent(r scanner) (Agent, error) {
	var a Agent
	var paired, seen string
	if err := r.Scan(&a.AgentID, &a.Hostname, &a.LastIP, &a.Port, &a.FP, &a.Role, &a.Version,
		&paired, &seen, &a.State, &a.FreeBytes); err != nil {
		return Agent{}, err
	}
	var err error
	if a.PairedAt, err = parseTime(paired); err != nil {
		return Agent{}, err
	}
	if a.LastSeen, err = parseTime(seen); err != nil {
		return Agent{}, err
	}
	return a, nil
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s)
}

// RecordPoll stores what a successful /status poll showed.
func (s *Store) RecordPoll(ctx context.Context, id, state, version string, freeBytes int64, at time.Time) error {
	return s.update(ctx, id, `UPDATE agents SET state = ?, version = ?, free_bytes = ?, last_seen = ? WHERE agent_id = ?`,
		state, version, freeBytes, fmtTime(at), id)
}

// SetAgentState changes only the state (offline, error); the last poll's data stays.
func (s *Store) SetAgentState(ctx context.Context, id, state string) error {
	return s.update(ctx, id, `UPDATE agents SET state = ? WHERE agent_id = ?`, state, id)
}
