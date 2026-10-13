package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Policy is a schedule plus how many backups carrying its tag each node keeps
// (blueprint: Backup engine → Policies and tiers).
type Policy struct {
	Name         string
	Cron         string // "" never fires: the policy is used only through 'backup now --tag'
	Keep         int
	Destinations []string // agent_ids; empty = every paired node
	Enabled      bool     // false stops scheduling; retention still applies Keep
}

const policyCols = `name, cron, keep, destinations, enabled`

func (s *Store) Policies(ctx context.Context) ([]Policy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+policyCols+` FROM policies ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Policy
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Policy(ctx context.Context, name string) (Policy, error) {
	p, err := scanPolicy(s.db.QueryRowContext(ctx, `SELECT `+policyCols+` FROM policies WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return Policy{}, fmt.Errorf("policy %s: %w", name, ErrNotFound)
	}
	return p, err
}

func (s *Store) InsertPolicy(ctx context.Context, p Policy) error {
	dest, err := destinations(p)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO policies (`+policyCols+`) VALUES (?,?,?,?,?) ON CONFLICT(name) DO NOTHING`,
		p.Name, p.Cron, p.Keep, dest, p.Enabled)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("policy %s: %w", p.Name, ErrExists)
	}
	return nil
}

func (s *Store) UpdatePolicy(ctx context.Context, p Policy) error {
	dest, err := destinations(p)
	if err != nil {
		return err
	}
	return s.affect(ctx, "policy "+p.Name, `UPDATE policies SET cron = ?, keep = ?, destinations = ?, enabled = ? WHERE name = ?`,
		p.Cron, p.Keep, dest, p.Enabled, p.Name)
}

func (s *Store) DeletePolicy(ctx context.Context, name string) error {
	return s.affect(ctx, "policy "+name, `DELETE FROM policies WHERE name = ?`, name)
}

// affect runs a statement that must touch a row; none is ErrNotFound for what.
func (s *Store) affect(ctx context.Context, what, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("%s: %w", what, ErrNotFound)
	}
	return nil
}

func destinations(p Policy) (string, error) {
	if p.Destinations == nil {
		p.Destinations = []string{}
	}
	b, err := json.Marshal(p.Destinations)
	return string(b), err
}

func scanPolicy(r scanner) (Policy, error) {
	var p Policy
	var dest string
	if err := r.Scan(&p.Name, &p.Cron, &p.Keep, &dest, &p.Enabled); err != nil {
		return Policy{}, err
	}
	return p, json.Unmarshal([]byte(dest), &p.Destinations)
}
