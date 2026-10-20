package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

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
