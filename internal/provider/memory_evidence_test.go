package provider

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const memoryEvidenceAttempt = "memory-query"

func TestMemoryCredentialQueryNeverEntersProtocolEvidence(t *testing.T) {
	ctx := context.Background()
	evidence, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := evidence.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err = evidence.Put(ctx, "sessions", memoryEvidenceAttempt, model.Attempt{AttemptID: memoryEvidenceAttempt}, 0); err != nil {
		t.Fatal(err)
	}
	query := "Investigate API_KEY=private-memory-query-credential"
	var received string
	session := providerSession{hooks: Hooks{
		Event: func(kind string, value any) { captureMemoryProtocolEvent(t, evidence, kind, value) },
		SearchLessons: func(_ context.Context, value string) (string, error) {
			received = value
			return "", errors.New("remove credentials from memory query")
		},
	}}
	params, _ := json.Marshal(map[string]any{"tool": "search_lessons", "arguments": map[string]string{"query": query}})
	result, err := session.handle(ctx, Message{Method: "item/tool/call", ID: json.RawMessage(`1`), Params: params})
	if err != nil {
		t.Fatal(err)
	}
	if received != query {
		t.Fatalf("callback received %q; want original query", received)
	}
	reply, _ := result.(map[string]any)
	if reply["success"] != false {
		t.Fatalf("query rejection lost: %+v", result)
	}
	assertMemoryProtocolArtifactsOmitQuery(t, evidence, query)
}

func captureMemoryProtocolEvent(t *testing.T, evidence *store.Store, kind string, value any) {
	t.Helper()
	body, err := store.SafeJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	_, err = evidence.Capture(context.Background(), memoryEvidenceAttempt, kind, "application/json", body, store.MessagePayloadLimit, false)
	if err != nil {
		t.Fatal(err)
	}
}

func assertMemoryProtocolArtifactsOmitQuery(t *testing.T, evidence *store.Store, query string) {
	t.Helper()
	ctx := context.Background()
	events, err := evidence.AttemptEvents(ctx, memoryEvidenceAttempt, 0, 100)
	if err != nil || len(events) == 0 {
		t.Fatalf("protocol events: %d %v", len(events), err)
	}
	for _, event := range events {
		_, body, err := evidence.Artifact(ctx, event.Artifact.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertMemoryQueryOmitted(t, body, query)
	}
}

func assertMemoryQueryOmitted(t *testing.T, body []byte, query string) {
	t.Helper()
	text := string(body)
	if strings.Contains(text, query) || strings.Contains(text, "private-memory-query-credential") {
		t.Fatalf("credential query persisted: %s", text)
	}
	if !strings.Contains(text, "search_lessons") || !strings.Contains(text, "query omitted") {
		t.Fatalf("memory call provenance missing: %s", text)
	}
}
