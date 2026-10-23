package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
	"whirlwind/internal/model"
)

func cancelModeWork(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT kind,id,data,version FROM resources WHERE kind IN ('tasks','sessions','operations','mutations','verification','publications','questions','permissions','budget_reservations')")
	if err != nil {
		return err
	}
	changes, err := modeCancellationChanges(rows, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	for _, change := range changes {
		if err = put(ctx, tx, change.Kind, change.ID, change.Value, change.Expected); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, "UPDATE leases SET expires=0")
	return err
}
func modeCancellationChanges(rows *sql.Rows, now string) (changes []Change, resultErr error) {
	defer func() {
		if err := rows.Close(); resultErr == nil {
			resultErr = err
		}
	}()
	for rows.Next() {
		var kind, id string
		var data []byte
		var version int
		if err := rows.Scan(&kind, &id, &data, &version); err != nil {
			return nil, err
		}
		value, changed, err := modeCancellationValue(kind, data, now)
		if err != nil {
			return nil, err
		}
		if changed {
			changes = append(changes, Change{kind, id, value, version})
		}
	}
	return changes, rows.Err()
}
func modeCancellationValue(kind string, data []byte, now string) (any, bool, error) {
	if kind == "tasks" {
		return cancelledModeTask(data)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, false, err
	}
	changed := cancelModeOperation(kind, value, now)
	if changed {
		value["reason"] = "Workflow mode changed"
	}
	return value, changed, nil
}
func cancelledModeTask(data []byte) (any, bool, error) {
	var task model.Task
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, false, err
	}
	if task.Stage == "complete" || task.Cancelled() {
		return task, false, nil
	}
	task.Stage = "cancelled"
	task.ResumeStage = ""
	task.Error = "Cancelled by workflow mode change"
	task.Approval = nil
	task.Approved = false
	task.Wait = nil
	task.Handoff.NextAction = "Start new work explicitly; cancelled tasks cannot resume"
	return task, true, nil
}
func cancelModeOperation(kind string, value map[string]any, now string) bool {
	switch kind {
	case "sessions":
		return cancelModeSession(value, now)
	case "questions":
		return cancelPending(value, "status")
	case "permissions":
		return cancelPending(value, "decision")
	case "budget_reservations":
		return releaseModeBudget(value)
	default:
		return cancelModeStage(value, now)
	}
}
func cancelModeSession(value map[string]any, now string) bool {
	if value["finished"] != false {
		return false
	}
	value["finished"] = true
	value["status"] = "cancelled"
	value["finished_at"] = now
	return true
}
func releaseModeBudget(value map[string]any) bool {
	if value["status"] != "active" {
		return false
	}
	value["status"] = "uncertain_released"
	return true
}
func cancelModeStage(value map[string]any, now string) bool {
	switch value["stage"] {
	case "running", "pending", "uncertain":
		value["stage"] = "cancelled"
		value["finished_at"] = now
		return true
	default:
		return false
	}
}
