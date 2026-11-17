package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"whirlwind/internal/model"
)

// Operations retain their existing identity; this projection supports durable
// intake independently from the optional periodic schedule.
func syncIdeationRunTx(ctx context.Context, tx *sql.Tx, id string, data []byte) error {
	var op model.Operation
	if err := json.Unmarshal(data, &op); err != nil {
		return err
	}
	if op.Kind != "ideation" {
		return nil
	}
	status := op.Stage
	switch status {
	case "complete":
		status = "completed"
	case "failed", "interrupted":
		status = "error"
	case "queued", "running", "completed", "partial", "error", "paused":
	default:
		return ErrConflict
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO ideation_runs(run_id,project_id,status,created_at,finished_at,error_reason,repository_revision,configuration_hash)
	VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET status=excluded.status,finished_at=excluded.finished_at,error_reason=excluded.error_reason,repository_revision=excluded.repository_revision,configuration_hash=excluded.configuration_hash
	WHERE ideation_runs.project_id=excluded.project_id AND (ideation_runs.repository_revision='' OR ideation_runs.repository_revision=excluded.repository_revision) AND (ideation_runs.configuration_hash='' OR ideation_runs.configuration_hash=excluded.configuration_hash)`, id, op.Subject, status, op.StartedAt, op.FinishedAt, op.Error, op.RepositoryRevision, op.ConfigurationHash)
	if err != nil {
		return err
	}
	if err := requireOneRow(result); err != nil {
		return err
	}
	for _, direction := range []string{"expand", "optimize", "harden", "simplify"} {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO ideation_directions(run_id,direction,status) VALUES(?,?,'queued')`, id, direction); err != nil {
			return err
		}
	}

	if status == "completed" || status == "partial" || status == "error" {
		reason := op.Error
		if reason == "" {
			reason = "run ended without a qualifying idea for this direction"
		}
		if _, err := tx.ExecContext(ctx, "UPDATE ideation_directions SET status='error',error_reason=? WHERE run_id=? AND status<>'completed'", reason, id); err != nil {
			return err
		}
	}
	return nil
}
