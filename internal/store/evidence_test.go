package store

import (
	"context"
	"encoding/json"
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

func TestEvidenceMigrationPreservesHistoricalRecords(t *testing.T) {
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
	for _, query := range []string{"DROP TABLE attempt_events", "DROP TABLE artifacts", "DELETE FROM migrations WHERE version >= 3"} {
		if _, err = s.DB.Exec(query); err != nil {
			t.Fatal(err)
		}
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
		t.Fatal("migration invented historical data", record, err)
	}
	events, err := s.AttemptEvents(ctx, "historical", 0, 100)
	if err != nil || len(events) != 0 {
		t.Fatal("migration invented historical events", events, err)
	}
	var version int
	if err = s.DB.QueryRow("SELECT max(version) FROM migrations").Scan(&version); err != nil || version != 4 {
		t.Fatal(version, err)
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
func TestRecoveryPreservesAnswersAndClosesOrphanedTransport(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, c := range []Change{{"sessions", "a", map[string]any{"finished": false, "owner": "old"}, 0}, {"operations", "o", map[string]any{"stage": "running", "kind": "provider_test"}, 0}, {"questions", "pending", map[string]any{"status": "pending", "attempt_id": "a"}, 0}, {"questions", "answered", map[string]any{"status": "answered", "answers": map[string]string{"q": "keep"}}, 0}, {"permissions", "p", map[string]any{"decision": "pending", "attempt_id": "a"}, 0}} {
		if err := s.Put(ctx, c.Kind, c.ID, c.Value, c.Expected); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecoverInterrupted(ctx, "new"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ kind, id, field, want string }{{"sessions", "a", "status", "interrupted"}, {"operations", "o", "stage", "failed"}, {"questions", "pending", "status", "cancelled"}, {"questions", "answered", "status", "answered"}, {"permissions", "p", "decision", "cancelled"}} {
		r, _ := s.Get(ctx, tc.kind, tc.id)
		var v map[string]any
		_ = json.Unmarshal(r.Data, &v)
		if v[tc.field] != tc.want {
			t.Errorf("%s: %s", tc.id, r.Data)
		}
	}
}

func TestEvidenceExcludesSecretsAndPrivateThoughts(t *testing.T) {
	data, err := SafeJSON(map[string]any{"accessToken": "private-camel-token", "authorization": "Bearer private-token", "command": []string{"agent", "--api-key", "private-key", "--token=private-inline"}, "env": map[string]string{"PASSWORD": "private-password"}, "content": []any{map[string]any{"type": "thinking", "thinking": "private-thought"}, map[string]any{"type": "text", "text": "visible text"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-token", "private-key", "private-password", "private-thought", "private-inline", "private-camel-token"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("retained %s", secret)
		}
	}
	if !strings.Contains(string(data), "visible text") {
		t.Fatal("visible evidence lost")
	}
}

func TestEssentialMetadataSurvivesPayloadBudget(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.Put(ctx, "sessions", "a", map[string]bool{"finished": false}, 0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := s.Capture(ctx, "a", "patch", "text/plain", []byte(strings.Repeat("x", PatchPayloadLimit)), PatchPayloadLimit, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Capture(ctx, "a", "whirlwind/process_exit", evidenceJSONType, []byte(`{"session_id":"native-id","exit_code":12}`), MessagePayloadLimit, false); err != nil {
		t.Fatal(err)
	}
	var meta string
	if err := s.DB.QueryRow("SELECT metadata FROM attempt_events WHERE kind='whirlwind/process_exit'").Scan(&meta); err != nil || !strings.Contains(meta, "native-id") || !strings.Contains(meta, "12") {
		t.Fatal("essential outcome lost after cap", meta, err)
	}
	if _, err := s.Capture(ctx, "a", "whirlwind/tool_result", evidenceJSONType, []byte(`{"reply":{"result":{"terminalId":"terminal-one","exitStatus":{"exitCode":12,"signal":"SIGTERM"}}}}`), MessagePayloadLimit, false); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow("SELECT metadata FROM attempt_events WHERE kind='whirlwind/tool_result'").Scan(&meta); err != nil || !strings.Contains(meta, "terminal-one") || !strings.Contains(meta, "12") || !strings.Contains(meta, "SIGTERM") {
		t.Fatal("ACP outcome lost after cap", meta, err)
	}
}

func TestBoundedBinaryAndUTF8Prefixes(t *testing.T) {
	binary := []byte{'x', 0xff, 'y', 'z'}
	if got := Prefix(binary, 3); len(got) != 3 || got[1] != 0xff {
		t.Fatal("binary prefix lost", got)
	}
	if got := string(Prefix([]byte("hi😊done"), 4)); got != "hi" {
		t.Fatal("partial UTF8 rune retained", got)
	}
}

func TestStreamedMessageHasOneMessageBudget(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.Put(ctx, "sessions", "a", map[string]bool{"finished": false}, 0); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"params": map[string]any{"itemId": "message-one", "delta": strings.Repeat("x", MessagePayloadLimit/2)}})
	for i := 0; i < 4; i++ {
		if _, err := s.Capture(ctx, "a", "item/agentMessage/delta", evidenceJSONType, data, MessagePayloadLimit, false); err != nil {
			t.Fatal(err)
		}
	}
	var size int
	if err := s.DB.QueryRow("SELECT sum(stored_bytes) FROM artifacts WHERE attempt_id='a'").Scan(&size); err != nil || size != MessagePayloadLimit {
		t.Fatal("stream exceeds per-message budget", size, err)
	}
}
