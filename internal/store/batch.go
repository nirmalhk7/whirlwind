package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
	"whirlwind/internal/model"
)

type Change struct {
	Kind, ID string
	Value    any
	Expected int
}
type Receipt struct {
	Key, Fingerprint string
	Status           int
	Body             []byte
}
type batchKey struct{}

// Batch stages request writes without holding a database transaction during external work.
type Batch struct {
	changes []Change
	records map[string]model.Record
	after   []func()
	at      string
}

func Stage(ctx context.Context) (context.Context, *Batch) {
	b := &Batch{records: map[string]model.Record{}, at: time.Now().UTC().Format(time.RFC3339Nano)}
	return context.WithValue(ctx, batchKey{}, b), b
}
func staged(ctx context.Context) *Batch { b, _ := ctx.Value(batchKey{}).(*Batch); return b }
func AfterCommit(ctx context.Context, f func()) {
	if b := staged(ctx); b != nil {
		b.after = append(b.after, f)
	} else {
		f()
	}
}
func (b *Batch) put(ctx context.Context, s *Store, c Change) error {
	record, err := s.Get(ctx, c.Kind, c.ID)
	if err = checkExpectedVersion(record, err, c.Expected); err != nil {
		return err
	}
	data, err := json.Marshal(c.Value)
	if err != nil {
		return err
	}
	b.changes = append(b.changes, c)
	b.records[c.Kind+":"+c.ID] = model.Record{ID: c.ID, Version: c.Expected + 1, Data: data, UpdatedAt: b.at, CreatedOrder: record.CreatedOrder}
	return nil
}

func checkExpectedVersion(record model.Record, err error, expected int) error {
	if expected == 0 {
		if err == nil {
			return ErrConflict
		}
		if errors.Is(err, ErrNotFound) {
			return nil
		}
	} else if err == nil && record.Version != expected {
		return ErrConflict
	}
	return err
}
func (b *Batch) Commit(ctx context.Context, s *Store, receipt *Receipt, extra ...Change) error {
	changes := append(append([]Change{}, b.changes...), extra...)
	if err := s.Write(ctx, changes, receipt); err != nil {
		return err
	}
	for _, f := range b.after {
		f()
	}
	return nil
}

// Write commits aggregate changes, their SSE events, and optional HTTP receipt together.
func (s *Store) Write(ctx context.Context, changes []Change, receipt *Receipt) error {
	if s.Source != nil && !bypassSource(ctx) {
		for _, c := range changes {
			if ConfigurationKind(c.Kind) {
				return s.Source.Persist(ctx, changes, func() error { return s.write(ctx, changes, receipt) })
			}
		}
	}
	return s.write(ctx, changes, receipt)
}
func (s *Store) write(ctx context.Context, changes []Change, receipt *Receipt) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		for _, c := range changes {
			if err := put(ctx, tx, c.Kind, c.ID, c.Value, c.Expected); err != nil {
				return err
			}
		}
		if receipt != nil {
			_, err := tx.ExecContext(ctx, "INSERT INTO idempotency(key,fingerprint,status,body) VALUES(?,?,?,?)", receipt.Key, receipt.Fingerprint, receipt.Status, receipt.Body)
			return err
		}
		return nil
	})
}
func (s *Store) Audit(ctx context.Context, a model.Audit) error {
	a.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return s.Put(ctx, "audit", ID(), a, 0)
}
