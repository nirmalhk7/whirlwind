package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// put performs a version-checked resource write inside an existing transaction and appends the matching event.
func put(ctx context.Context, tx *sql.Tx, kind, id string, v any, expected int) error {
	if _, removal := v.(directionRemoval); removal {
		return removeDirection(ctx, tx, id, expected)
	}
	if _, remove := v.(ConfigurationRemoval); remove {
		return removeConfiguration(ctx, tx, kind, id, expected)
	}
	if _, removal := v.(projectRemoval); removal {
		return removeProject(ctx, tx, id, expected)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var before []byte
	if kind == "tasks" {
		err = tx.QueryRowContext(ctx, "SELECT data FROM resources WHERE kind='tasks' AND id=?", id).Scan(&before)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
	}
	if err = writeResource(ctx, tx, Change{kind, id, v, expected}, b); err != nil {
		return err
	}
	if kind == "tasks" {
		return notifyTaskTransition(ctx, tx, before, b)
	}
	return nil
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
