package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFreshStoreHasNoSchemaUpgradeHistory(t *testing.T) {
	database := openTest(t)
	var count int
	if err := database.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='migrations'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("fresh state must use the current schema directly, without migration history")
	}
}

func TestCurrentSchemaPreservesArtifactStreamsOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Put(ctx, "sessions", "a", map[string]bool{"finished": true}, 0); err != nil {
		t.Fatal(err)
	}
	artifact, err := database.Capture(ctx, "a", "message", "text/plain", []byte("saved evidence"), MessagePayloadLimit, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.Exec("UPDATE artifacts SET stream_key='existing-stream' WHERE id=?", artifact.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var stream string
	if err := database.DB.QueryRow("SELECT stream_key FROM artifacts WHERE id=?", artifact.ID).Scan(&stream); err != nil || stream != "existing-stream" {
		t.Fatal("reopening changed artifact stream:", stream, err)
	}
	_, content, err := database.Artifact(ctx, artifact.ID)
	if err != nil || string(content) != "saved evidence" {
		t.Fatal("reopening changed artifact content:", string(content), err)
	}
}
