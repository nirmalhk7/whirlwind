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
	"strings"
	"time"

	"khan/internal/model"
)

var ErrConflict = errors.New("version conflict")
var ErrNotFound = errors.New("not found")
var ErrLeased = errors.New("lease already held")

type Store struct{ DB *sql.DB }
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
	s := &Store{db}
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", "CREATE TABLE IF NOT EXISTS migrations(version INTEGER PRIMARY KEY)"} {
		if _, e = db.Exec(q); e != nil {
			db.Close()
			return nil, e
		}
	}
	if e = s.migrate(); e != nil {
		db.Close()
		return nil, e
	}

	return s, nil
}

// Close closes the SQLite connection pool.
func (s *Store) Close() error { return s.DB.Close() }

// Get loads one versioned resource and returns ErrNotFound when its kind and ID do not exist.
func (s *Store) Get(ctx context.Context, kind, id string) (model.Record, error) {
	r := model.Record{ID: id}
	var data []byte
	e := s.DB.QueryRowContext(ctx, "SELECT version,data,updated_at FROM resources WHERE kind=? AND id=?", kind, id).Scan(&r.Version, &data, &r.UpdatedAt)
	r.Data = json.RawMessage(data)
	if errors.Is(e, sql.ErrNoRows) {
		e = ErrNotFound
	}
	return r, e
}

// List loads all resources of a kind in update-time and ID order.
func (s *Store) List(ctx context.Context, kind string) ([]model.Record, error) {
	rows, e := s.DB.QueryContext(ctx, "SELECT id,version,data,updated_at FROM resources WHERE kind=? ORDER BY updated_at,id", kind)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Record{}
	for rows.Next() {
		var r model.Record
		var data []byte
		if e = rows.Scan(&r.ID, &r.Version, &data, &r.UpdatedAt); e != nil {
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

// put performs a version-checked resource write inside an existing transaction and appends the matching event.
func put(ctx context.Context, tx *sql.Tx, kind, id string, v any, expected int) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var res sql.Result
	if expected == 0 {
		res, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO resources VALUES(?,?,1,?,?)", kind, id, string(b), now)
	} else {
		res, e = tx.ExecContext(ctx, "UPDATE resources SET version=version+1,data=?,updated_at=? WHERE kind=? AND id=? AND version=?", string(b), now, kind, id, expected)
	}
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO events(kind,resource_id,data,created_at) VALUES(?,?,?,?)", kind, id, string(b), now)
	return e
}

// Events loads ordered events after a cursor, capped by the requested limit.
func (s *Store) Events(ctx context.Context, after int64, limit int) ([]model.Event, error) {
	rows, e := s.DB.QueryContext(ctx, "SELECT id,kind,resource_id,data,created_at FROM events WHERE id>? ORDER BY id LIMIT ?", after, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
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

// IngestIssue deduplicates repository issues, binds published planner tasks, pauses withdrawn issues, and invalidates stale plans after edits.
func (s *Store) IngestIssue(ctx context.Context, p model.Project, i model.Issue) (model.Task, error) {
	var t model.Task
	e := s.Transaction(ctx, func(tx *sql.Tx) error {
		var id string
		e := tx.QueryRowContext(ctx, "SELECT task_id FROM issue_tasks WHERE repo=? AND issue=?", p.Repo, i.Number).Scan(&id)
		if errors.Is(e, sql.ErrNoRows) {
			if !i.Eligible() {
				return ErrNotFound
			}
			marker := "<!-- khan-plan:"
			if begin := strings.Index(i.Body, marker); begin >= 0 {
				rest := i.Body[begin+len(marker):]
				if end := strings.Index(rest, " -->"); end >= 0 {
					candidateID := rest[:end]
					var data []byte
					var revision int
					if x := tx.QueryRowContext(ctx, "SELECT data,version FROM resources WHERE kind='tasks' AND id=?", candidateID).Scan(&data, &revision); x == nil {
						var candidate model.Task
						if json.Unmarshal(data, &candidate) == nil && candidate.ProjectID == p.ID && candidate.Issue.Number == 0 && candidate.Stage == "plan_ready" && candidate.Plan != nil {
							candidate.Issue = i
							candidate.Stage = "coding"
							t = candidate
							if _, x = tx.ExecContext(ctx, "INSERT INTO issue_tasks(repo,issue,task_id) VALUES(?,?,?)", p.Repo, i.Number, candidateID); x != nil {
								return x
							}
							return put(ctx, tx, "tasks", candidateID, candidate, revision)
						}
					}
				}
			}
			t = model.Task{ID: ID(), ProjectID: p.ID, Issue: i, PlanVersion: 1, Stage: "planning", Handoff: model.Handoff{Version: 1, NextAction: "plan issue"}}
			if _, e = tx.ExecContext(ctx, "INSERT INTO issue_tasks(repo,issue,task_id) VALUES(?,?,?)", p.Repo, i.Number, t.ID); e != nil {
				return e
			}
			return put(ctx, tx, "tasks", t.ID, t, 0)
		}
		if e != nil {
			return e
		}
		var b []byte
		var version int
		if e = tx.QueryRowContext(ctx, "SELECT data,version FROM resources WHERE kind='tasks' AND id=?", id).Scan(&b, &version); e != nil {
			return e
		}
		if e = json.Unmarshal(b, &t); e != nil {
			return e
		}
		incoming, parseIncoming := time.Parse(time.RFC3339, i.UpdatedAt)
		existing, parseExisting := time.Parse(time.RFC3339, t.Issue.UpdatedAt)
		if parseIncoming == nil && parseExisting == nil && incoming.Before(existing) {
			return nil
		}
		if !i.Eligible() {
			if t.EligibilityOverride && t.Issue.Body == i.Body && t.Issue.Title == i.Title {
				return nil
			}
			if t.Stage == "paused" {
				return nil
			}
			t.ResumeStage = t.Stage
			t.Stage = "paused"
			t.Error = "Issue closed or khan:plan removed"
			t.Issue = i
			return put(ctx, tx, "tasks", id, t, version)
		}
		if !t.Issue.Eligible() && t.Stage == "paused" && t.Error == "Issue closed or khan:plan removed" && t.Issue.Body == i.Body && t.Issue.Title == i.Title {
			t.Stage = t.ResumeStage
			if t.Stage == "" {
				t.Stage = "planning"
			}
			t.Issue = i
			t.Error = ""
			t.EligibilityOverride = false
			return put(ctx, tx, "tasks", id, t, version)
		}
		if t.Issue.UpdatedAt == i.UpdatedAt && t.Issue.Body == i.Body && t.Issue.Title == i.Title {
			return nil
		}
		if t.PR != "" {
			t.Error = "Issue changed after PR creation; review published PR in GitHub"
			t.ResumeStage = t.Stage
			t.Stage = "paused"
		} else {
			t.PlanVersion++
			t.ResumeStage = ""
			t.EligibilityOverride = false
			t.ReviewedCommit = ""
			t.Plan = nil
			t.Worktree = ""
			t.Branch = ""
			t.Base = ""
			t.ReviewPasses = 0
			t.Recoveries = 0
			t.UnchangedFailures = 0
			t.Fingerprint = ""
			t.Stage = "planning"
			t.ChecksPassed = false
			t.Approved = false
			t.Score = 0
			t.Handoff = model.Handoff{Version: 1, NextAction: "replan updated issue"}
		}
		t.Issue = i
		return put(ctx, tx, "tasks", id, t, version)
	})
	return t, e
}

// Claim claims an absent or expired lease and increments its fencing token; an unexpired lease returns ErrLeased.
func (s *Store) Claim(ctx context.Context, id, owner string, ttl time.Duration) (Lease, error) {
	var l Lease
	e := s.Transaction(ctx, func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		var old Lease
		e := tx.QueryRowContext(ctx, "SELECT owner,fence,expires FROM leases WHERE id=?", id).Scan(&old.Owner, &old.Fence, &old.ExpiresAt)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if old.ExpiresAt > now {
			return ErrLeased
		}
		l = Lease{owner, old.Fence + 1, now + ttl.Milliseconds()}
		_, e = tx.ExecContext(ctx, "INSERT INTO leases VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET owner=excluded.owner,fence=excluded.fence,expires=excluded.expires", id, l.Owner, l.Fence, l.ExpiresAt)
		return e
	})
	return l, e
}

// Renew extends a live lease only when its owner and fencing token still match.
func (s *Store) Renew(ctx context.Context, id, owner string, fence int64, ttl time.Duration) error {
	res, e := s.DB.ExecContext(ctx, "UPDATE leases SET expires=? WHERE id=? AND owner=? AND fence=? AND expires>?", time.Now().Add(ttl).UnixMilli(), id, owner, fence, time.Now().UnixMilli())
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrLeased
	}
	return nil
}

// Release expires a lease only for its current owner and fencing token.
func (s *Store) Release(ctx context.Context, id, owner string, fence int64) error {
	res, e := s.DB.ExecContext(ctx, "UPDATE leases SET expires=0 WHERE id=? AND owner=? AND fence=?", id, owner, fence)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrLeased
	}
	return nil
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

// BindIssue atomically associates a planner task with its published GitHub issue.
func (s *Store) BindIssue(ctx context.Context, repo string, t model.Task, expected int) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		var other string
		err := tx.QueryRowContext(ctx, "SELECT task_id FROM issue_tasks WHERE repo=? AND issue=?", repo, t.Issue.Number).Scan(&other)
		if err == nil && other != t.ID {
			return ErrConflict
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO issue_tasks(repo,issue,task_id) VALUES(?,?,?)", repo, t.Issue.Number, t.ID); err != nil {
			return err
		}
		return put(ctx, tx, "tasks", t.ID, t, expected)
	})
}

// SaveDirections stores the generated directions and marks their project/day schedule complete in one transaction.
func (s *Store) SaveDirections(ctx context.Context, project, day string, directions []model.Direction) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		for _, d := range directions {
			if err := put(ctx, tx, "directions", d.ID, d, 0); err != nil {
				return err
			}
		}
		key := project + ":" + day
		var version int
		err := tx.QueryRowContext(ctx, "SELECT version FROM resources WHERE kind='schedules' AND id=?", key).Scan(&version)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return put(ctx, tx, "schedules", key, map[string]string{"stage": "complete", "day": day, "project": project}, version)
	})
}

// migrate applies missing schema versions transactionally and rejects databases newer than this server.
func (s *Store) migrate() error {
	migrations := [][]string{
		{`CREATE TABLE resources(kind TEXT NOT NULL,id TEXT NOT NULL,version INTEGER NOT NULL CHECK(version>0),data TEXT NOT NULL CHECK(json_valid(data)),updated_at TEXT NOT NULL,PRIMARY KEY(kind,id))`, `CREATE TABLE events(id INTEGER PRIMARY KEY AUTOINCREMENT,kind TEXT NOT NULL,resource_id TEXT NOT NULL,data TEXT NOT NULL,created_at TEXT NOT NULL)`, `CREATE TABLE issue_tasks(repo TEXT NOT NULL,issue INTEGER NOT NULL,task_id TEXT NOT NULL UNIQUE,PRIMARY KEY(repo,issue))`, `CREATE TABLE leases(id TEXT PRIMARY KEY,owner TEXT NOT NULL,fence INTEGER NOT NULL,expires INTEGER NOT NULL)`, `CREATE TABLE deliveries(id TEXT PRIMARY KEY,received_at TEXT NOT NULL)`, `CREATE TABLE idempotency(key TEXT PRIMARY KEY,fingerprint TEXT NOT NULL,status INTEGER NOT NULL,body BLOB NOT NULL)`},
		{`ALTER TABLE issue_tasks RENAME TO issue_tasks_v1`, `CREATE TABLE issue_tasks(repo TEXT NOT NULL,issue INTEGER NOT NULL,task_id TEXT NOT NULL UNIQUE,task_kind TEXT NOT NULL DEFAULT 'tasks' CHECK(task_kind='tasks'),PRIMARY KEY(repo,issue),FOREIGN KEY(task_kind,task_id) REFERENCES resources(kind,id) DEFERRABLE INITIALLY DEFERRED)`, `INSERT INTO issue_tasks(repo,issue,task_id) SELECT repo,issue,task_id FROM issue_tasks_v1`, `DROP TABLE issue_tasks_v1`},
	}
	var current int
	if err := s.DB.QueryRow("SELECT COALESCE(MAX(version),0) FROM migrations").Scan(&current); err != nil {
		return err
	}
	if current > len(migrations) {
		return errors.New("database schema newer than server")
	}
	for n := current; n < len(migrations); n++ {
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		for _, sql := range migrations[n] {
			if _, err = tx.Exec(sql); err != nil {
				tx.Rollback()
				return err
			}
		}
		if _, err = tx.Exec("INSERT INTO migrations(version) VALUES(?)", n+1); err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
