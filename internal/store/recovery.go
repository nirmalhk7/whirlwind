package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
	"whirlwind/internal/model"
)

// RecoverInterrupted is called only after acquiring the exclusive server lock and reaping registered children.
func (s *Store) RecoverInterrupted(ctx context.Context, owner string) error {
	changes := []Change{}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, kind := range []string{"sessions", "operations", "verification", "publications", "mutations", "budget_reservations", "recovery_episodes"} {
		records, err := s.List(ctx, kind)
		if err != nil {
			return err
		}
		for _, r := range records {
			change, err := interruptedRecord(kind, r, owner, now)
			if err != nil {
				return err
			}
			if change != nil {
				changes = append(changes, *change)
			}
		}
	}
	return s.commitRecovery(ctx, owner, changes)
}

func interruptedRecord(kind string, record model.Record, owner, now string) (*Change, error) {
	var value map[string]any
	if err := json.Unmarshal(record.Data, &value); err != nil {
		return nil, err
	}
	if value["owner"] == owner || !interruptValue(kind, value, now) {
		return nil, nil
	}
	return &Change{kind, record.ID, value, record.Version}, nil
}

func interruptValue(kind string, value map[string]any, now string) bool {
	var changed bool
	switch kind {
	case "budget_reservations":
		if value["status"] == "active" {
			value["status"] = "uncertain"
			changed = true
		}
	case "sessions":
		changed = interruptSession(value, now)
	case "operations", "verification", "mutations":
		changed = interruptOperation(value, now)
	case "publications":
		changed = interruptPublication(value)
	case "recovery_episodes":
		changed = interruptRecoveryEpisode(value, now)
	}
	return changed
}

func interruptRecoveryEpisode(value map[string]any, now string) bool {
	if value["status"] != "running" {
		return false
	}
	value["status"] = "interrupted"
	if raw, ok := value["restart_count"].(float64); ok {
		restart := int(raw) + 1
		delays := [...]time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second}
		if restart >= 1 && restart < len(delays) {
			started, err := time.Parse(time.RFC3339Nano, now)
			if err == nil {
				value["next_retry_at"] = started.Add(delays[restart]).Format(time.RFC3339Nano)
			}
		}
	}
	value["updated_at"] = now
	return true
}

func interruptSession(value map[string]any, now string) bool {
	if value["finished"] != false {
		return false
	}
	value["finished"] = true
	value["status"] = "interrupted"
	value["finished_at"] = now
	value["error"] = "Server restarted before attempt completion"
	delete(value, "success")
	return true
}

func interruptOperation(value map[string]any, now string) bool {
	if value["stage"] != "running" {
		return false
	}
	value["stage"] = "failed"
	value["failure_kind"] = "interrupted"
	value["finished_at"] = now
	value["error"] = "Server restarted; retry required"
	return true
}

func interruptPublication(value map[string]any) bool {
	if value["stage"] != "running" {
		return false
	}
	value["stage"] = "uncertain"
	value["error"] = "Server restarted; reconcile remote result before retry"
	return true
}

func cancelPending(value map[string]any, field string) bool {
	if value[field] != "pending" {
		return false
	}
	value[field] = "cancelled"
	value["reason"] = "Owning transport interrupted"
	return true
}

func (s *Store) commitRecovery(ctx context.Context, owner string, changes []Change) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		for _, c := range changes {
			if err := put(ctx, tx, c.Kind, c.ID, c.Value, c.Expected); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, "UPDATE leases SET expires=0 WHERE owner<>?", owner)
		return err
	})
}
