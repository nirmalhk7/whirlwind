package store

import (
	"context"
	"database/sql"
	"time"

	"whirlwind/internal/model"
)

type duplicateProjectArchive struct{}
type checkoutRegistrationPrecondition struct{ path string }

// UnregisteredCheckoutPrecondition prevents a concurrent registration or the
// legacy origin-derived ID of a registered checkout from creating two rows.
func UnregisteredCheckoutPrecondition(project model.Project) Change {
	return Change{Kind: "projects", ID: project.ID, Value: checkoutRegistrationPrecondition{path: project.Path}}
}

func assertCheckoutUnregistered(ctx context.Context, tx *sql.Tx, path string) error {
	var registered bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM projects p JOIN resources r ON r.kind='projects' AND r.id=p.project_id WHERE p.project_path=?)", path).Scan(&registered); err != nil {
		return err
	}
	if registered {
		return ErrConflict
	}
	return nil
}

// ArchiveDuplicateProject retires a duplicate registration while preserving its
// original data and all history referring to its ID.
func ArchiveDuplicateProject(record model.Record) Change {
	return Change{Kind: "projects", ID: record.ID, Value: duplicateProjectArchive{}, Expected: record.Version}
}

func archiveDuplicateProject(ctx context.Context, tx *sql.Tx, id string, version int) error {
	result, err := tx.ExecContext(ctx, `INSERT INTO resources(kind,id,version,data,updated_at)
		SELECT 'archived_projects',id,version,data,updated_at FROM resources
		WHERE kind='projects' AND id=? AND version=?
		ON CONFLICT(kind,id) DO UPDATE SET version=excluded.version,data=excluded.data,updated_at=excluded.updated_at`, id, version)
	if err != nil {
		return err
	}
	if err := requireOneRow(result); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM resources WHERE kind='projects' AND id=?", id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO events(kind,resource_id,data,created_at) VALUES('projects',?,?,?)", id, `{"deleted":true}`, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
