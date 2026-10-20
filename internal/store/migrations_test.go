package store

import (
	"context"
	"path/filepath"
	"testing"
)

const artifactContentType = "text/plain"

func TestUpgradeVersionFourWithoutArtifactStreamKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	artifactID := createVersionFourArtifactDB(t, path)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Capture(ctx, "historical", "message", artifactContentType, []byte("new evidence"), MessagePayloadLimit, false); err != nil {
			_ = s.Close()
			t.Fatal("capture after upgrade:", err)
		}
		_, content, err := s.Artifact(ctx, artifactID)
		if err != nil || string(content) != "saved evidence" {
			t.Fatal("historical evidence changed:", string(content), err)
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func createVersionFourArtifactDB(t *testing.T, path string) string {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = s.Put(ctx, "sessions", "historical", map[string]bool{"finished": true}, 0); err != nil {
		t.Fatal(err)
	}
	artifact, err := s.Capture(ctx, "historical", "message", artifactContentType, []byte("saved evidence"), MessagePayloadLimit, false)
	if err != nil {
		t.Fatal(err)
	}
	// Earlier version-four databases only had the event metadata upgrade.
	for _, query := range []string{
		"DELETE FROM migrations WHERE version > 4",
		"DROP INDEX artifacts_stream",
		"ALTER TABLE artifacts DROP COLUMN stream_key",
	} {
		if _, err = s.DB.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	return artifact.ID
}

func TestUpgradeVersionFourPreservesArtifactStreamKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "streams.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = s.Put(ctx, "sessions", "a", map[string]bool{"finished": true}, 0); err != nil {
		t.Fatal(err)
	}
	artifact, err := s.Capture(ctx, "a", "message", artifactContentType, []byte("saved"), MessagePayloadLimit, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE artifacts SET stream_key='existing-stream' WHERE id=?", artifact.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("DELETE FROM migrations WHERE version > 4"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var stream string
	if err = s.DB.QueryRow("SELECT stream_key FROM artifacts WHERE id=?", artifact.ID).Scan(&stream); err != nil || stream != "existing-stream" {
		t.Fatal("stream key changed:", stream, err)
	}
}
