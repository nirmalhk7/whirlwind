package store

import (
	"database/sql"
	_ "embed"
	"strings"
)

//go:embed schema.sql
var currentSchema string

// initializeSchema creates the current layout atomically. Obsolete layouts are
// discarded, not migrated; reopening the current layout preserves its state.
func (s *Store) initializeSchema() (err error) {
	var obsolete bool
	if err = s.DB.QueryRow(`SELECT
	 EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='execution_plans') OR
	 (EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name IN ('resources','projects','ideas','plans')) AND (
	  COALESCE((SELECT group_concat(name,',') FROM (SELECT name FROM pragma_table_info('ideas') ORDER BY cid)),'')<>'idea_id,project_id,run_id,idea_title,idea_type,what,why,where,how,status,timestamp' OR
	  COALESCE((SELECT group_concat(name,',') FROM (SELECT name FROM pragma_table_info('projects') ORDER BY cid)),'')<>'project_id,project_name,project_path,status,timestamp' OR
	  COALESCE((SELECT group_concat(name,',') FROM (SELECT name FROM pragma_table_info('plans') ORDER BY cid)),'')<>'plan_id,idea_id,plan_path,status,timestamp'
	 ))`).Scan(&obsolete); err != nil {
		return err
	}
	if obsolete {
		// SQLite requires disabling FK enforcement outside the reset transaction.
		// Open owns the sole connection; enforcement is restored before it returns.
		if _, err = s.DB.Exec("PRAGMA foreign_keys=OFF"); err != nil {
			return err
		}
		defer func() {
			_, restoreErr := s.DB.Exec("PRAGMA foreign_keys=ON")
			if err == nil {
				err = restoreErr
			}
		}()
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if obsolete {
		if err = discardObsoleteSchema(tx); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(currentSchema); err != nil {
		return err
	}
	return tx.Commit()
}

func discardObsoleteSchema(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT name,type FROM pragma_table_list WHERE schema='main'
	 AND name NOT LIKE 'sqlite_%' AND type IN ('table','virtual','view')`)
	if err != nil {
		return err
	}
	type object struct{ name, kind string }
	var objects []object
	for rows.Next() {
		var item object
		if err := rows.Scan(&item.name, &item.kind); err != nil {
			_ = rows.Close()
			return err
		}
		objects = append(objects, item)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	for _, item := range objects {
		kind := "TABLE"
		if item.kind == "view" {
			kind = "VIEW"
		}
		if _, err := tx.Exec(`DROP ` + kind + ` "` + strings.ReplaceAll(item.name, `"`, `""`) + `"`); err != nil {
			return err
		}
	}
	return nil
}
