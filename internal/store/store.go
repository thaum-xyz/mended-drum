// Package store persists the bar's mutable state (3-state ingredient
// inventory) in a local SQLite database.
package store

import (
	"context"
	"database/sql"
	"errors"
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
);
CREATE TABLE IF NOT EXISTS guest (
	handle_key TEXT PRIMARY KEY,
	handle     TEXT NOT NULL,
	notes      TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS guest_pref (
	handle_key TEXT NOT NULL,
	kind       TEXT NOT NULL CHECK (kind IN ('like','dislike','allergy')),
	value      TEXT NOT NULL,
	PRIMARY KEY (handle_key, kind, value)
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

type PrefKind string

const (
	Like    PrefKind = "like"
	Dislike PrefKind = "dislike"
	Allergy PrefKind = "allergy"
)

// ValidPrefKind reports whether s is an accepted preference kind.
func ValidPrefKind(s string) bool {
	switch PrefKind(s) {
	case Like, Dislike, Allergy:
		return true
	}
	return false
}

type Guest struct {
	Handle    string   `json:"handle"`
	Notes     string   `json:"notes,omitempty"`
	Likes     []string `json:"likes"`
	Dislikes  []string `json:"dislikes"`
	Allergies []string `json:"allergies"`
}

// UpsertGuest creates or updates a guest's profile notes.
func (s *Store) UpsertGuest(ctx context.Context, handle, notes string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO guest (handle_key, handle, notes, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(handle_key) DO UPDATE SET handle=excluded.handle, notes=excluded.notes`,
		Key(handle), strings.TrimSpace(handle), strings.TrimSpace(notes),
		time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *Store) ensureGuest(ctx context.Context, handle string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO guest (handle_key, handle, notes, created_at) VALUES (?, ?, '', ?)`,
		Key(handle), strings.TrimSpace(handle), time.Now().UTC().Format(time.RFC3339))
	return err
}

// AddPreference records a like/dislike/allergy for a guest, creating the guest
// if needed.
func (s *Store) AddPreference(ctx context.Context, handle, kind, value string) error {
	if err := s.ensureGuest(ctx, handle); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO guest_pref (handle_key, kind, value) VALUES (?, ?, ?)`,
		Key(handle), kind, strings.TrimSpace(value))
	return err
}

// GetGuest returns a guest with preferences grouped by kind, or nil if unknown.
func (s *Store) GetGuest(ctx context.Context, handle string) (*Guest, error) {
	g := &Guest{Likes: []string{}, Dislikes: []string{}, Allergies: []string{}}
	err := s.db.QueryRowContext(ctx,
		`SELECT handle, notes FROM guest WHERE handle_key = ?`, Key(handle)).Scan(&g.Handle, &g.Notes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT kind, value FROM guest_pref WHERE handle_key = ?`, Key(handle))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, value string
		if err := rows.Scan(&kind, &value); err != nil {
			return nil, err
		}
		switch PrefKind(kind) {
		case Like:
			g.Likes = append(g.Likes, value)
		case Dislike:
			g.Dislikes = append(g.Dislikes, value)
		case Allergy:
			g.Allergies = append(g.Allergies, value)
		}
	}
	return g, rows.Err()
}

// SearchGuests returns guests whose handle or notes match q (all if q is empty).
func (s *Store) SearchGuests(ctx context.Context, q string) ([]Guest, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	var (
		rows *sql.Rows
		err  error
	)
	if q == "" {
		rows, err = s.db.QueryContext(ctx, `SELECT handle, notes FROM guest ORDER BY handle`)
	} else {
		like := "%" + q + "%"
		rows, err = s.db.QueryContext(ctx,
			`SELECT handle, notes FROM guest WHERE lower(handle) LIKE ? OR lower(notes) LIKE ? ORDER BY handle`,
			like, like)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Guest{}
	for rows.Next() {
		var g Guest
		if err := rows.Scan(&g.Handle, &g.Notes); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
