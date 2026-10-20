package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"time"

	"whirlwind/internal/model"
)

var ErrConflict = errors.New("version conflict")
var ErrNotFound = errors.New("not found")
var ErrPersistence = errors.New("required persistence failed")
var ErrLeased = errors.New("lease already held")

type Store struct {
	DB     *sql.DB
	Source SettingsSource
}
type Lease struct {
	Owner     string
	Fence     int64
	ExpiresAt int64
}

// ID generates a random 24-character hexadecimal resource identifier; entropy failure panics.
func ID() string {
	var b [12]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}

// Open opens SQLite with one connection, enables WAL and foreign keys, and applies pending schema migrations.
func Open(path string) (*Store, error) {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", "CREATE TABLE IF NOT EXISTS migrations(version INTEGER PRIMARY KEY)"} {
		if _, e = db.Exec(q); e != nil {
			_ = db.Close()
			return nil, e
		}
	}
	if e = s.migrate(); e != nil {
		_ = db.Close()
		return nil, e
	}

	return s, nil
}

// Close closes the SQLite connection pool.
func (s *Store) Close() error { return s.DB.Close() }

// Get loads one versioned resource and returns ErrNotFound when its kind and ID do not exist.
func (s *Store) Get(ctx context.Context, kind, id string) (model.Record, error) {
	if ConfigurationKind(kind) {
		if err := s.RefreshSettings(ctx); err != nil {
			return model.Record{}, err
		}
	}
	if b := staged(ctx); b != nil {
		if r, ok := b.records[kind+":"+id]; ok {
			if r.Data == nil {
				return r, ErrNotFound
			}
			return r, nil
		}
	}
	r := model.Record{ID: id}
	var data []byte
	e := s.DB.QueryRowContext(ctx, "SELECT rowid,version,data,updated_at FROM resources WHERE kind=? AND id=?", kind, id).Scan(&r.CreatedOrder, &r.Version, &data, &r.UpdatedAt)
	r.Data = json.RawMessage(data)
	if errors.Is(e, sql.ErrNoRows) {
		e = ErrNotFound
	}
	return r, e
}

// List loads all resources of a kind in update-time and ID order.
func (s *Store) List(ctx context.Context, kind string) ([]model.Record, error) {
	if ConfigurationKind(kind) {
		if err := s.RefreshSettings(ctx); err != nil {
			return nil, err
		}
	}
	rows, e := s.DB.QueryContext(ctx, "SELECT rowid,id,version,data,updated_at FROM resources WHERE kind=? ORDER BY updated_at,id", kind)
	if e != nil {
		return nil, e
	}
	defer func() { _ = rows.Close() }()
	out := []model.Record{}
	for rows.Next() {
		var r model.Record
		var data []byte
		if e = rows.Scan(&r.CreatedOrder, &r.ID, &r.Version, &data, &r.UpdatedAt); e != nil {
			return nil, e
		}
		r.Data = json.RawMessage(data)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Decode unmarshals a stored resource payload into the requested Go type.
func Decode[T any](r model.Record) (T, error) { var v T; e := json.Unmarshal(r.Data, &v); return v, e }

// Put creates or updates a resource at its expected version and records its event in the same transaction.
func (s *Store) Put(ctx context.Context, kind, id string, value any, expected int) error {
	if b := staged(ctx); b != nil {
		return b.put(ctx, s, Change{kind, id, value, expected})
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error { return put(ctx, tx, kind, id, value, expected) })
}

// Transaction commits the callback atomically and retries SQLite busy or locked errors up to three attempts.
func (s *Store) Transaction(ctx context.Context, f func(*sql.Tx) error) error {
	for n := 0; n < 3; n++ {
		tx, e := s.DB.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		e = f(tx)
		if e == nil {
			e = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if e == nil {
			return nil
		}
		if !isBusy(e) {
			return e
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(n+1) * 25 * time.Millisecond):
		}
	}
	return errors.New("database busy after retries")
}

// isBusy identifies SQLite busy and locked errors that can be retried.
func isBusy(e error) bool {
	type code interface {
		// Code returns the SQLite error number used to identify retryable lock failures.
		Code() int
	}
	var c code
	return errors.As(e, &c) && (c.Code() == 5 || c.Code() == 6)
}

// Events loads ordered events after a cursor, capped by the requested limit.
func (s *Store) Events(ctx context.Context, after int64, limit int) ([]model.Event, error) {
	rows, e := s.DB.QueryContext(ctx, "SELECT id,kind,resource_id,data,created_at FROM events WHERE id>? ORDER BY id LIMIT ?", after, limit)
	if e != nil {
		return nil, e
	}
	defer func() { _ = rows.Close() }()
	out := []model.Event{}
	for rows.Next() {
		var v model.Event
		var data []byte
		if e = rows.Scan(&v.ID, &v.Kind, &v.ResourceID, &data, &v.CreatedAt); e != nil {
			return nil, e
		}
		v.Data = json.RawMessage(data)
		out = append(out, v)
	}
	return out, rows.Err()
}

// Delivery records a webhook delivery ID and reports whether this is its first receipt.
func (s *Store) Delivery(ctx context.Context, id string) (bool, error) {
	r, e := s.DB.ExecContext(ctx, "INSERT OR IGNORE INTO deliveries VALUES(?,?)", id, time.Now().UTC().Format(time.RFC3339))
	if e != nil {
		return false, e
	}
	n, e := r.RowsAffected()
	return n == 1, e
}

// Notify stores an unresolved Inbox notification and emits its resource event.
func (s *Store) Notify(ctx context.Context, task, kind, message string) error {
	n := model.Notification{ID: ID(), TaskID: task, Kind: kind, Message: message}
	return s.Put(ctx, "inbox", n.ID, n, 0)
}

// Idempotent returns a saved mutation response, or ErrConflict when a key is reused with different request content.
func (s *Store) Idempotent(ctx context.Context, key, fingerprint string) (int, []byte, error) {
	var f string
	var status int
	var b []byte
	e := s.DB.QueryRowContext(ctx, "SELECT fingerprint,status,body FROM idempotency WHERE key=?", key).Scan(&f, &status, &b)
	if errors.Is(e, sql.ErrNoRows) {
		return 0, nil, nil
	}
	if e != nil {
		return 0, nil, e
	}
	if f != fingerprint {
		return 0, nil, fmt.Errorf("%w: idempotency key reused", ErrConflict)
	}
	return status, b, nil
}

// Remember saves a mutation response under its idempotency key and request fingerprint.
func (s *Store) Remember(ctx context.Context, key, f string, status int, b []byte) error {
	_, e := s.DB.ExecContext(ctx, "INSERT INTO idempotency VALUES(?,?,?,?)", key, f, status, b)
	return e
}
