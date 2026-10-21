package store

import (
	"context"
	"database/sql"
	"errors"
	"whirlwind/internal/model"
)

type MemoryBudget struct {
	AutomaticBytes int               `json:"automatic_bytes"`
	Calls          int               `json:"calls"`
	Bytes          int               `json:"bytes"`
	Seen           []model.LessonRef `json:"seen"`
}

func (s *Store) MemoryAllowance(ctx context.Context, id string) (MemoryBudget, error) {
	record, err := s.Get(ctx, "memory_budgets", id)
	if errors.Is(err, ErrNotFound) {
		return MemoryBudget{}, nil
	}
	if err != nil {
		return MemoryBudget{}, err
	}
	return Decode[MemoryBudget](record)
}

func (s *Store) RecordAutomaticMemory(ctx context.Context, id string, refs []model.LessonRef, bytes int) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		record, err := lessonRecord(ctx, tx, "memory_budgets", id)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		budget := MemoryBudget{}
		if record.Version > 0 {
			budget, err = Decode[MemoryBudget](record)
			if err != nil {
				return err
			}
		}
		if err := budget.validateAutomatic(refs, bytes); err != nil {
			return err
		}
		budget.AutomaticBytes += bytes
		budget.Seen = append(budget.Seen, refs...)
		return put(ctx, tx, "memory_budgets", id, budget, record.Version)
	})
}

// ReserveMemorySearch consumes one of two calls atomically, including failed searches.
func (s *Store) ReserveMemorySearch(ctx context.Context, id string) (MemoryBudget, error) {
	var budget MemoryBudget
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		budget = MemoryBudget{}
		r, err := lessonRecord(ctx, tx, "memory_budgets", id)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if r.Version > 0 {
			budget, err = Decode[MemoryBudget](r)
			if err != nil {
				return err
			}
		}
		if budget.Calls >= 2 {
			return errors.New("memory search allowance exhausted")
		}
		budget.Calls++
		return put(ctx, tx, "memory_budgets", id, budget, r.Version)
	})
	return budget, err
}

func (s *Store) RecordMemoryDelivery(ctx context.Context, id string, refs []model.LessonRef, bytes int) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		r, err := lessonRecord(ctx, tx, "memory_budgets", id)
		if err != nil {
			return err
		}
		b, err := Decode[MemoryBudget](r)
		if err != nil {
			return err
		}
		if bytes < 0 || bytes > 2048 || b.Bytes+bytes > 4096 {
			return errors.New("memory response budget exceeded")
		}
		if err := b.validateUnseen(refs); err != nil {
			return err
		}
		b.Seen = append(b.Seen, refs...)
		b.Bytes += bytes
		return put(ctx, tx, "memory_budgets", id, b, r.Version)
	})
}

func (budget MemoryBudget) validateAutomatic(refs []model.LessonRef, bytes int) error {
	if bytes < 0 || budget.AutomaticBytes+bytes > 2048 {
		return ErrConflict
	}
	return budget.validateUnseen(refs)
}

func (budget MemoryBudget) validateUnseen(refs []model.LessonRef) error {
	for _, ref := range refs {
		for _, seen := range budget.Seen {
			if ref.ID == seen.ID && ref.Version == seen.Version {
				return ErrConflict
			}
		}
	}
	return nil
}
