package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestReopenDropsObsoleteMigrationHistoryWithoutResettingCurrentRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Put(ctx, "sessions", "kept", map[string]bool{"finished": true}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.Exec("CREATE TABLE migrations(version INTEGER PRIMARY KEY); INSERT INTO migrations VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='migrations'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("reopening retained obsolete migration history")
	}
	record, err := database.Get(ctx, "sessions", "kept")
	if err != nil || record.Version != 1 || string(record.Data) != `{"finished":true}` {
		t.Fatalf("removing obsolete history reset current records: %+v %v", record, err)
	}
}
