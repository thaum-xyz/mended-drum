// Package store persists the bar's mutable state (3-state ingredient inventory
// and guest profiles/preferences) in PostgreSQL.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
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

// Open connects to PostgreSQL (dsn is a postgres:// URL), waits for it to be
// reachable, and migrates the schema.
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	var pingErr error
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		pingErr = db.PingContext(ctx)
		cancel()
		if pingErr == nil {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if pingErr != nil {
		return nil, fmt.Errorf("ping postgres: %w", pingErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Key normalises an ingredient or guest name into its storage key.
func Key(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// Set creates or updates the stock status for an ingredient.
func (s *Store) Set(ctx context.Context, name, status string) (Stock, error) {
	display := strings.TrimSpace(name)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ingredient_stock (food_key, display_name, status, updated_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (food_key) DO UPDATE SET
		   display_name = EXCLUDED.display_name,
		   status       = EXCLUDED.status,
		   updated_at   = EXCLUDED.updated_at`,
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
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (handle_key) DO UPDATE SET handle = EXCLUDED.handle, notes = EXCLUDED.notes`,
		Key(handle), strings.TrimSpace(handle), strings.TrimSpace(notes),
		time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *Store) ensureGuest(ctx context.Context, handle string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO guest (handle_key, handle, notes, created_at) VALUES ($1, $2, '', $3)
		 ON CONFLICT (handle_key) DO NOTHING`,
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
		`INSERT INTO guest_pref (handle_key, kind, value) VALUES ($1, $2, $3)
		 ON CONFLICT DO NOTHING`,
		Key(handle), kind, strings.TrimSpace(value))
	return err
}

// GetGuest returns a guest with preferences grouped by kind, or nil if unknown.
func (s *Store) GetGuest(ctx context.Context, handle string) (*Guest, error) {
	g := &Guest{Likes: []string{}, Dislikes: []string{}, Allergies: []string{}}
	err := s.db.QueryRowContext(ctx,
		`SELECT handle, notes FROM guest WHERE handle_key = $1`, Key(handle)).Scan(&g.Handle, &g.Notes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	g.Likes, g.Dislikes, g.Allergies, err = s.loadPrefs(ctx, Key(handle))
	if err != nil {
		return nil, err
	}
	return g, nil
}

// loadPrefs returns a guest's preferences grouped by kind.
func (s *Store) loadPrefs(ctx context.Context, handleKey string) (likes, dislikes, allergies []string, err error) {
	likes, dislikes, allergies = []string{}, []string{}, []string{}
	rows, err := s.db.QueryContext(ctx, `SELECT kind, value FROM guest_pref WHERE handle_key = $1`, handleKey)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, value string
		if err := rows.Scan(&kind, &value); err != nil {
			return nil, nil, nil, err
		}
		switch PrefKind(kind) {
		case Like:
			likes = append(likes, value)
		case Dislike:
			dislikes = append(dislikes, value)
		case Allergy:
			allergies = append(allergies, value)
		}
	}
	return likes, dislikes, allergies, rows.Err()
}

// SearchGuests returns guests (with preferences) whose handle or notes match q;
// all guests if q is empty.
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
			`SELECT handle, notes FROM guest WHERE lower(handle) LIKE $1 OR lower(notes) LIKE $2 ORDER BY handle`,
			like, like)
	}
	if err != nil {
		return nil, err
	}
	out := []Guest{}
	for rows.Next() {
		var g Guest
		if err := rows.Scan(&g.Handle, &g.Notes); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	for i := range out {
		out[i].Likes, out[i].Dislikes, out[i].Allergies, err = s.loadPrefs(ctx, Key(out[i].Handle))
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
