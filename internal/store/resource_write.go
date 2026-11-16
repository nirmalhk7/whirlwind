package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
	"whirlwind/internal/model"
)

// put performs a version-checked resource write inside an existing transaction and appends the matching event.
func put(ctx context.Context, tx *sql.Tx, kind, id string, v any, expected int) error {
	if handled, err := putRemoval(ctx, tx, kind, id, v, expected); handled {
		return err
	}
	return putResource(ctx, tx, kind, id, v, expected)
}

func putResource(ctx context.Context, tx *sql.Tx, kind, id string, v any, expected int) error {
	if (kind == "plan_versions" || kind == "finalization_intents" || kind == "finalizations") && expected != 0 {
		return ErrConflict
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	before, err := previousTaskData(ctx, tx, kind, id)
	if err != nil {
		return err
	}
	if kind == "ideas" {
		data, err = prepareIdea(id, before, data)
		if err != nil {
			return err
		}
	}
	if err = writeResource(ctx, tx, Change{kind, id, v, expected}, data); err != nil {
		return err
	}
	return notifyResourceChange(ctx, tx, kind, id, v, before, data)
}

func previousTaskData(ctx context.Context, tx *sql.Tx, kind, id string) ([]byte, error) {
	if kind != "execution_plans" && kind != "ideas" && kind != "sessions" {
		return nil, nil
	}
	var before []byte
	err := tx.QueryRowContext(ctx, "SELECT data FROM resources WHERE kind=? AND id=?", kind, id).Scan(&before)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	return before, nil
}

func notifyResourceChange(ctx context.Context, tx *sql.Tx, kind, id string, value any, before, data []byte) error {
	if kind == "projects" || kind == "project_status" {
		_, err := tx.ExecContext(ctx, `UPDATE projects SET status=CASE WHEN EXISTS (
		 SELECT 1 FROM resources r,json_each(r.data) health
		 WHERE r.kind='project_status' AND
		 (json_extract(health.value,'$.project_id')=projects.project_id OR json_extract(health.value,'$.path')=projects.project_path OR json_extract(health.value,'$.canonical_path')=projects.project_path)
		 AND json_extract(health.value,'$.status') IN ('ERR','error')
		) THEN 'error' WHEN status='complete' THEN status ELSE COALESCE((SELECT json_extract(r.data,'$.status') FROM resources r WHERE r.kind='projects' AND r.id=projects.project_id),'draft') END`)
		return err
	}
	if kind == "sessions" {
		return syncPlannerAttemptTx(ctx, tx, id, before, data)
	}
	if kind == "operations" {
		return syncIdeationRunTx(ctx, tx, id, data)
	}
	if kind == "ideas" {
		return syncIdeaTx(ctx, tx, id, data)
	}
	if kind == "execution_plans" {
		if err := syncExecutionPlanTx(ctx, tx, id, data); err != nil {
			return err
		}
		if err := syncPlannerPublicationTx(ctx, tx, before, data); err != nil {
			return err
		}
		return notifyTaskTransition(ctx, tx, before, data)
	}
	if kind == "plan_versions" {
		return syncPlanVersionTx(ctx, tx, id, data)
	}
	if kind == "directions" {
		if direction, ok := value.(model.Direction); ok && direction.ProjectID != "" {
			if direction.ID == "" {
				direction.ID = id
			}
			var canonicalProject string
			err := tx.QueryRowContext(ctx, "SELECT project_id FROM ideas WHERE idea_id=?", direction.ID).Scan(&canonicalProject)
			if err == nil {
				if canonicalProject != direction.ProjectID {
					return ErrConflict
				}
				return nil
			}
			if err != sql.ErrNoRows {
				return err
			}
			return syncProposalTx(ctx, tx, direction)
		}
	}
	return nil
}

func putRemoval(ctx context.Context, tx *sql.Tx, kind, id string, value any, expected int) (bool, error) {
	switch value.(type) {
	case resourceVersionAssertion:
		return true, assertResourceVersionTx(ctx, tx, kind, id, expected)
	case directionRemoval:
		return true, removeDirection(ctx, tx, id, expected)
	case ConfigurationRemoval:
		return true, removeConfiguration(ctx, tx, kind, id, expected)
	case projectRemoval:
		return true, removeProject(ctx, tx, id, expected)
	default:
		return false, nil
	}
}

func writeResource(ctx context.Context, tx *sql.Tx, change Change, data []byte) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if batch := staged(ctx); batch != nil {
		now = batch.at
	}
	var result sql.Result
	var err error
	if change.Expected == 0 {
		result, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO resources VALUES(?,?,1,?,?)", change.Kind, change.ID, string(data), now)
	} else {
		result, err = tx.ExecContext(ctx, "UPDATE resources SET version=version+1,data=?,updated_at=? WHERE kind=? AND id=? AND version=?", string(data), now, change.Kind, change.ID, change.Expected)
	}
	if err != nil {
		return err
	}
	if err = requireOneRow(result); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO events(kind,resource_id,data,created_at) VALUES(?,?,?,?)", change.Kind, change.ID, string(data), now)
	return err
}

func removeConfiguration(ctx context.Context, tx *sql.Tx, kind, id string, expected int) error {
	if !ConfigurationKind(kind) {
		return ErrConflict
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM resources WHERE kind=? AND id=? AND version=?", kind, id, expected)
	if err != nil {
		return err
	}
	if err = requireOneRow(result); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO events(kind,resource_id,data,created_at) VALUES(?,?,?,?)", kind, id, `{"deleted":true}`, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func requireOneRow(result sql.Result) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
