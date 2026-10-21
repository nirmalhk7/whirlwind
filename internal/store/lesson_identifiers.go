package store

// Structured identifiers stay separate from prose so exact matches do not scan lesson JSON.
func lessonIdentifierMigration() []string {
	values := `SELECT new.rowid,json_extract(new.data,'$.project_id'),json_extract(new.data,'$.role'),'path',lower(value),2 FROM json_each(new.data,'$.paths') WHERE length(value)>0 UNION SELECT new.rowid,json_extract(new.data,'$.project_id'),json_extract(new.data,'$.role'),'symbol',lower(value),1 FROM json_each(new.data,'$.symbols') WHERE length(value)>0 UNION SELECT new.rowid,json_extract(new.data,'$.project_id'),json_extract(new.data,'$.role'),'error',lower(json_extract(new.data,'$.error_signature')),3 WHERE length(COALESCE(json_extract(new.data,'$.error_signature'),''))>0`
	return []string{
		`CREATE TABLE IF NOT EXISTS lesson_identifiers(lesson INTEGER NOT NULL,project TEXT NOT NULL,role TEXT NOT NULL,kind TEXT NOT NULL,value TEXT NOT NULL,weight INTEGER NOT NULL,PRIMARY KEY(lesson,kind,value))`,
		`CREATE INDEX IF NOT EXISTS lesson_identifiers_scope ON lesson_identifiers(project,role)`,
		`CREATE TRIGGER IF NOT EXISTS lesson_identifier_insert AFTER INSERT ON resources WHEN new.kind='lessons' BEGIN INSERT INTO lesson_identifiers ` + values + `; END`,
		`CREATE TRIGGER IF NOT EXISTS lesson_identifier_update AFTER UPDATE ON resources WHEN new.kind='lessons' BEGIN DELETE FROM lesson_identifiers WHERE lesson=old.rowid; INSERT INTO lesson_identifiers ` + values + `; END`,
		`CREATE TRIGGER IF NOT EXISTS lesson_identifier_delete AFTER DELETE ON resources WHEN old.kind='lessons' BEGIN DELETE FROM lesson_identifiers WHERE lesson=old.rowid; END`,
		`DELETE FROM lesson_identifiers`,
		`INSERT INTO lesson_identifiers SELECT r.rowid,json_extract(r.data,'$.project_id'),json_extract(r.data,'$.role'),'path',lower(p.value),2 FROM resources r,json_each(r.data,'$.paths') p WHERE r.kind='lessons' AND length(p.value)>0 UNION SELECT r.rowid,json_extract(r.data,'$.project_id'),json_extract(r.data,'$.role'),'symbol',lower(p.value),1 FROM resources r,json_each(r.data,'$.symbols') p WHERE r.kind='lessons' AND length(p.value)>0 UNION SELECT rowid,json_extract(data,'$.project_id'),json_extract(data,'$.role'),'error',lower(json_extract(data,'$.error_signature')),3 FROM resources WHERE kind='lessons' AND length(COALESCE(json_extract(data,'$.error_signature'),''))>0`,
	}
}
