package store

import (
	"database/sql"
	"errors"
)

// migrate applies missing schema versions transactionally and rejects databases newer than this server.
func (s *Store) migrate() error {
	migrations := [][]string{
		{`CREATE TABLE resources(kind TEXT NOT NULL,id TEXT NOT NULL,version INTEGER NOT NULL CHECK(version>0),data TEXT NOT NULL CHECK(json_valid(data)),updated_at TEXT NOT NULL,PRIMARY KEY(kind,id))`, `CREATE TABLE events(id INTEGER PRIMARY KEY AUTOINCREMENT,kind TEXT NOT NULL,resource_id TEXT NOT NULL,data TEXT NOT NULL,created_at TEXT NOT NULL)`, `CREATE TABLE issue_tasks(repo TEXT NOT NULL,issue INTEGER NOT NULL,task_id TEXT NOT NULL UNIQUE,PRIMARY KEY(repo,issue))`, `CREATE TABLE leases(id TEXT PRIMARY KEY,owner TEXT NOT NULL,fence INTEGER NOT NULL,expires INTEGER NOT NULL)`, `CREATE TABLE deliveries(id TEXT PRIMARY KEY,received_at TEXT NOT NULL)`, `CREATE TABLE idempotency(key TEXT PRIMARY KEY,fingerprint TEXT NOT NULL,status INTEGER NOT NULL,body BLOB NOT NULL)`},
		{`ALTER TABLE issue_tasks RENAME TO issue_tasks_v1`, `CREATE TABLE issue_tasks(repo TEXT NOT NULL,issue INTEGER NOT NULL,task_id TEXT NOT NULL UNIQUE,task_kind TEXT NOT NULL DEFAULT 'tasks' CHECK(task_kind='tasks'),PRIMARY KEY(repo,issue),FOREIGN KEY(task_kind,task_id) REFERENCES resources(kind,id) DEFERRABLE INITIALLY DEFERRED)`, `INSERT INTO issue_tasks(repo,issue,task_id) SELECT repo,issue,task_id FROM issue_tasks_v1`, `DROP TABLE issue_tasks_v1`},
		{`CREATE TABLE artifacts(id TEXT PRIMARY KEY,attempt_id TEXT NOT NULL,attempt_kind TEXT NOT NULL DEFAULT 'sessions' CHECK(attempt_kind IN ('sessions','verification')),kind TEXT NOT NULL,content_type TEXT NOT NULL,bytes INTEGER NOT NULL,stored_bytes INTEGER NOT NULL,sha256 TEXT NOT NULL,truncated INTEGER NOT NULL,created_at TEXT NOT NULL,content BLOB NOT NULL,FOREIGN KEY(attempt_kind,attempt_id) REFERENCES resources(kind,id))`, `CREATE INDEX artifacts_attempt ON artifacts(attempt_id)`, `CREATE TABLE attempt_events(sequence INTEGER PRIMARY KEY AUTOINCREMENT,attempt_id TEXT NOT NULL,kind TEXT NOT NULL,artifact_id TEXT NOT NULL REFERENCES artifacts(id),replay INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL)`, `CREATE INDEX attempt_events_cursor ON attempt_events(attempt_id,sequence)`},
		{`ALTER TABLE attempt_events ADD COLUMN metadata TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(metadata))`, `ALTER TABLE artifacts ADD COLUMN stream_key TEXT NOT NULL DEFAULT ''`, `CREATE INDEX artifacts_stream ON artifacts(attempt_id,stream_key)`},
		{`CREATE INDEX IF NOT EXISTS artifacts_stream ON artifacts(attempt_id,stream_key)`},
		{}, // Persist original idea batches and normalize their selected choice.
		{`CREATE INDEX IF NOT EXISTS lessons_scope ON resources(json_extract(data,'$.project_id'),json_extract(data,'$.role'),json_extract(data,'$.status')) WHERE kind='lessons'`},
		lessonSearchMigration(),
		lessonIdentifierMigration(),
	}
	var current int
	if err := s.DB.QueryRow("SELECT COALESCE(MAX(version),0) FROM migrations").Scan(&current); err != nil {
		return err
	}
	if current > len(migrations) {
		return errors.New("database schema newer than server")
	}
	for n := current; n < len(migrations); n++ {
		if err := s.applyMigration(n+1, migrations[n]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) applyMigration(version int, statements []string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := applyMigrationSteps(tx, version, statements); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO migrations(version) VALUES(?)", version); err != nil {
		return err
	}
	return tx.Commit()
}

func applyMigrationSteps(tx *sql.Tx, version int, statements []string) error {
	// Version four originally added only metadata. Some databases already
	// include stream_key; repair older ones without losing existing stream keys.
	if version == 5 {
		if err := ensureArtifactStreamKey(tx); err != nil {
			return err
		}
	}
	if version == 6 {
		if err := migrateDirectionGroups(tx); err != nil {
			return err
		}
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func ensureArtifactStreamKey(tx *sql.Tx) error {
	var columns int
	if err := tx.QueryRow("SELECT count(*) FROM pragma_table_info('artifacts') WHERE name='stream_key'").Scan(&columns); err != nil {
		return err
	}
	if columns == 0 {
		_, err := tx.Exec("ALTER TABLE artifacts ADD COLUMN stream_key TEXT NOT NULL DEFAULT ''")
		return err
	}
	return nil
}
