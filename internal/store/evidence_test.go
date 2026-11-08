package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

const evidenceJSONType = "application/json"

func TestEvidenceSchemaExists(t *testing.T) {
	s := openTest(t)
	for _, name := range []string{"attempt_events", "artifacts"} {
		var n int
		if err := s.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&n); err != nil || n != 1 {
			t.Errorf("missing durable %s table", name)
		}
	}
}

func TestReopenPreservesEvidenceRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "historical.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	payload := `{"role":"code","text":"historical output","finished":true}`
	if _, err = s.DB.Exec("INSERT INTO resources(kind,id,version,data,updated_at) VALUES('sessions','historical',7,?,'2025-01-02T03:04:05Z')", payload); err != nil {
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
	record, err := s.Get(ctx, "sessions", "historical")
	if err != nil || string(record.Data) != payload || record.Version != 7 || record.UpdatedAt != "2025-01-02T03:04:05Z" {
		t.Fatal("reopening changed saved data", record, err)
	}
	events, err := s.AttemptEvents(ctx, "historical", 0, 100)
	if err != nil || len(events) != 0 {
		t.Fatal("reopening invented events", events, err)
	}

}

func TestArtifactCapsAndOrderedReplay(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.Put(ctx, "sessions", "a", map[string]bool{"finished": false}, 0); err != nil {
		t.Fatal(err)
	}
	content := []byte(strings.Repeat("x", MessagePayloadLimit+17))
	for i := 0; i < 26; i++ {
		a, err := s.Capture(ctx, "a", "message", "text/plain", content, MessagePayloadLimit, i == 0)
		if err != nil {
			t.Fatal(err)
		}
		if !a.Truncated || a.StoredBytes > MessagePayloadLimit {
			t.Fatal(a)
		}
	}
	var total int
	if err := s.DB.QueryRow("SELECT sum(stored_bytes) FROM artifacts WHERE attempt_id='a'").Scan(&total); err != nil || total != AttemptPayloadLimit {
		t.Fatal(total, err)
	}
	first, err := s.AttemptEvents(ctx, "a", 0, 1)
	if err != nil || len(first) != 1 || !first[0].Replay {
		t.Fatal(first, err)
	}
	next, err := s.AttemptEvents(ctx, "a", first[0].Sequence, 1)
	if err != nil || len(next) != 1 || next[0].Sequence <= first[0].Sequence {
		t.Fatal(next, err)
	}
}
