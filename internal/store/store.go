// Package store persists the bar's mutable state (3-state ingredient
// inventory) in a local SQLite database.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Status string

const (
	InStock Status = "in_stock"
	Low     Status = "low"
	Out     Status = "out"
)

// ValidStatus reports whether s is an accepted stock status.
func ValidStatus(s string) bool {
	switch Status(s) {
	case InStock, Low, Out:
		return true
	}
	return false
}

type Stock struct {
	Name      string `json:"name"`
	Status    Status `json:"status"`
	UpdatedAt string `json:"updated_at"`
}

type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS ingredient_stock (
	food_key     TEXT PRIMARY KEY,
	display_name TEXT NOT NULL,
	status       TEXT NOT NULL CHECK (status IN ('in_stock','low','out')),
	updated_at   TEXT NOT NULL
);`

// Open opens (creating if needed) the SQLite database at path and migrates it.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// modernc.org/sqlite is safest with a single writer connection.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Key normalises an ingredient name into its inventory key.
func Key(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// Set creates or updates the stock status for an ingredient.
func (s *Store) Set(ctx context.Context, name, status string) (Stock, error) {
	display := strings.TrimSpace(name)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ingredient_stock (food_key, display_name, status, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(food_key) DO UPDATE SET
		   display_name = excluded.display_name,
		   status       = excluded.status,
		   updated_at   = excluded.updated_at`,
		Key(name), display, status, now)
	if err != nil {
		return Stock{}, err
	}
	return Stock{Name: display, Status: Status(status), UpdatedAt: now}, nil
}

// List returns all tracked ingredients ordered by name.
func (s *Store) List(ctx context.Context) ([]Stock, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT display_name, status, updated_at FROM ingredient_stock ORDER BY display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Stock
	for rows.Next() {
		var st Stock
		if err := rows.Scan(&st.Name, &st.Status, &st.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Statuses returns a map of normalised food key -> status, for join lookups.
func (s *Store) Statuses(ctx context.Context) (map[string]Status, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT food_key, status FROM ingredient_stock`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]Status)
	for rows.Next() {
		var k, st string
		if err := rows.Scan(&k, &st); err != nil {
			return nil, err
		}
		m[k] = Status(st)
	}
	return m, rows.Err()
}
