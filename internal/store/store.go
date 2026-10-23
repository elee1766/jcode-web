// Package store persists events and push subscriptions in SQLite.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	db *sql.DB
}

// Open opens the database at path and applies migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, mustSub(migrations, "migrations"))
	if err != nil {
		db.Close()
		return nil, err
	}
	if _, err := provider.Up(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

type Event struct {
	ID            int64
	UpstreamID    uint64
	SessionID     string
	SessionName   string
	SwarmID       string
	TimestampUnix int64
	Type          string
	PayloadJSON   string
	Summary       string
}

const eventCols = `id, upstream_id, session_id, session_name, swarm_id, timestamp_unix, type, payload_json, summary`

func scanEvents(rows *sql.Rows) ([]Event, error) {
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.UpstreamID, &e.SessionID, &e.SessionName, &e.SwarmID, &e.TimestampUnix, &e.Type, &e.PayloadJSON, &e.Summary); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// InsertEvent stores e and sets e.ID.
func (s *Store) InsertEvent(ctx context.Context, e *Event) error {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO events (upstream_id, session_id, session_name, swarm_id, timestamp_unix, type, payload_json, summary)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.UpstreamID, e.SessionID, e.SessionName, e.SwarmID, e.TimestampUnix, e.Type, e.PayloadJSON, e.Summary)
	if err != nil {
		return err
	}
	e.ID, err = res.LastInsertId()
	return err
}

// EventsAfter returns up to limit events with id > after, oldest first.
func (s *Store) EventsAfter(ctx context.Context, after int64, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+eventCols+` FROM events WHERE id > ? ORDER BY id ASC LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

// EventsBefore returns up to limit events with id < before (0 = latest),
// newest first. sessionID filters when set.
func (s *Store) EventsBefore(ctx context.Context, sessionID string, before int64, limit int) ([]Event, error) {
	q := `SELECT ` + eventCols + ` FROM events WHERE 1=1`
	var args []any
	if sessionID != "" {
		q += ` AND session_id = ?`
		args = append(args, sessionID)
	}
	if before > 0 {
		q += ` AND id < ?`
		args = append(args, before)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

// LastEventTimes returns the newest event timestamp per session.
func (s *Store) LastEventTimes(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id, MAX(timestamp_unix) FROM events GROUP BY session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var ts int64
		if err := rows.Scan(&id, &ts); err != nil {
			return nil, err
		}
		out[id] = ts
	}
	return out, rows.Err()
}

// PruneEvents deletes events older than maxAge.
func (s *Store) PruneEvents(ctx context.Context, maxAge time.Duration) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE timestamp_unix < ?`, time.Now().Add(-maxAge).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

type PushSubscription struct {
	Endpoint  string
	P256dh    string
	Auth      string
	UserAgent string
}

func (s *Store) UpsertPush(ctx context.Context, p PushSubscription) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO push_subscriptions (endpoint, p256dh, auth, user_agent, created_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(endpoint) DO UPDATE SET p256dh = excluded.p256dh, auth = excluded.auth, user_agent = excluded.user_agent`,
		p.Endpoint, p.P256dh, p.Auth, p.UserAgent, time.Now().Unix())
	return err
}

func (s *Store) DeletePush(ctx context.Context, endpoint string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}

func (s *Store) ListPush(ctx context.Context) ([]PushSubscription, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT endpoint, p256dh, auth, user_agent FROM push_subscriptions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSubscription
	for rows.Next() {
		var p PushSubscription
		if err := rows.Scan(&p.Endpoint, &p.P256dh, &p.Auth, &p.UserAgent); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ErrNotFound is returned by Get when the key is missing.
var ErrNotFound = errors.New("not found")

func (s *Store) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM kv WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

func (s *Store) Set(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
