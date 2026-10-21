package store

import "strings"

const lessonColumns = `rowid,id,project,role,trigger_text,mistake,correction,paths,symbols,error_signature`

func lessonIndexValues(alias string) string {
	fields := []string{"id", "project_id", "role", "trigger", "mistake", "correction", "paths", "symbols", "error_signature"}
	values := []string{alias + "rowid"}
	for _, field := range fields {
		values = append(values, "COALESCE(json_extract("+alias+"data,'$."+field+"'),'')")
	}
	return strings.Join(values, ",")
}

func lessonSearchMigration() []string {
	return []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS lesson_search USING fts5(id UNINDEXED,project UNINDEXED,role UNINDEXED,trigger_text,mistake,correction,paths,symbols,error_signature)`,
		`CREATE TRIGGER IF NOT EXISTS lesson_insert AFTER INSERT ON resources WHEN new.kind='lessons' BEGIN INSERT INTO lesson_search(` + lessonColumns + `) VALUES(` + lessonIndexValues("new.") + `); END`,
		`CREATE TRIGGER IF NOT EXISTS lesson_update AFTER UPDATE ON resources WHEN new.kind='lessons' BEGIN DELETE FROM lesson_search WHERE rowid=old.rowid; INSERT INTO lesson_search(` + lessonColumns + `) VALUES(` + lessonIndexValues("new.") + `); END`,
		`CREATE TRIGGER IF NOT EXISTS lesson_delete AFTER DELETE ON resources WHEN old.kind='lessons' BEGIN DELETE FROM lesson_search WHERE rowid=old.rowid; END`,
		`DELETE FROM lesson_search`,
		`INSERT INTO lesson_search(` + lessonColumns + `) SELECT ` + lessonIndexValues("") + ` FROM resources WHERE kind='lessons'`,
	}
}
