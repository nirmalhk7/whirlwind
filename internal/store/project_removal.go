package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"whirlwind/internal/model"
)

var ErrProjectBusy = errors.New("stop active tasks and wait for ideation to finish before deleting project")

type projectRemoval struct{}

// UnregisterProject removes only the registration, preserving repository files and history.
// Busy checks, deletion, events and the HTTP receipt commit in the same transaction.
func (s *Store) UnregisterProject(ctx context.Context, id string, expected int) error {
	if b := staged(ctx); b != nil {
		if err := b.put(ctx, s, Change{"projects", id, projectRemoval{}, expected}); err != nil {
			return err
		}
		b.records["projects:"+id] = model.Record{ID: id}
		return nil
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error { return removeProject(ctx, tx, id, expected) })
}

func removeProject(ctx context.Context, tx *sql.Tx, id string, expected int) error {
	var busy bool
	prefix := "ideation:" + id + ":"
	err := tx.QueryRowContext(ctx, `SELECT
 EXISTS(SELECT 1 FROM resources WHERE kind='tasks' AND json_extract(data,'$.project_id')=? AND json_extract(data,'$.stage') NOT IN ('paused','questions','approval','pr_created','complete')) OR
 EXISTS(SELECT 1 FROM resources WHERE kind='operations' AND json_extract(data,'$.kind')='ideation' AND json_extract(data,'$.subject')=? AND json_extract(data,'$.stage')='running') OR
 EXISTS(SELECT 1 FROM leases WHERE expires>? AND (id=? OR substr(id,1,length(?))=? OR id IN (SELECT 'task:'||id FROM resources WHERE kind='tasks' AND json_extract(data,'$.project_id')=?)))`, id, id, time.Now().UnixMilli(), "generation:"+id, prefix, prefix, id).Scan(&busy)
	if err != nil {
		return err
	}
	if busy {
		return ErrProjectBusy
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM resources WHERE kind='projects' AND id=? AND version=?", id, expected)
	if err != nil {
		return err
	}
	if err = requireOneRow(res); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO events(kind,resource_id,data,created_at) VALUES('projects',?,?,?)", id, `{"deleted":true}`, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
