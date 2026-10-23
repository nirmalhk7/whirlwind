package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestAgentConfigurationRequiresExplicitProviderModelAndVersion(t *testing.T) {
	s := setup(t)
	if err := s.Store.Put(context.Background(), "providers", "codex", model.Provider{ID: "codex", Kind: "codex", Capabilities: []string{"code"}}, 0); err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRecorder()
	s.Handler().ServeHTTP(get, httptest.NewRequest("GET", "http://localhost/api/v1/agents", nil))
	var agents []model.Record
	if get.Code != 200 || json.Unmarshal(get.Body.Bytes(), &agents) != nil || len(agents) != 5 {
		t.Fatalf("agent roles missing: %d %s", get.Code, get.Body)
	}
	for _, tc := range []struct {
		body, version string
		status        int
	}{
		{`{"chain":[{"provider":"codex","model":"selected-model"}]}`, "", 428},
		{`{"chain":[{"provider":"missing","model":"selected-model"}]}`, "0", 422},
		{`{"chain":[{"provider":"codex","model":""}]}`, "0", 422},
		{`{"chain":[{"provider":"codex","model":"selected-model"}]}`, "0", 200},
		{`{"chain":[{"provider":"codex","model":"other-model"}]}`, "0", 409},
	} {
		r := httptest.NewRequest("PATCH", "http://localhost/api/v1/agents/code", bytes.NewBufferString(tc.body))
		r.Header.Set("Idempotency-Key", store.ID())
		r.Header.Set("If-Match", tc.version)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("body %s version %s: %d %s", tc.body, tc.version, w.Code, w.Body)
		}
	}
}

func TestAgentThinkingValidationAndRoundTrip(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	_ = s.Store.Put(ctx, "providers", "acp", model.Provider{ID: "acp", Kind: "acp", Capabilities: []string{"code"}}, 0)
	for _, tc := range []struct {
		thinking string
		status   int
	}{
		{`{"config_id":"effort","value":""}`, 422},
		{`{"config_id":"","value":"high"}`, 422},
		{`{"config_id":"effort","value":"high"}`, 200},
	} {
		body := `{"chain":[{"provider":"acp","model":"m","thinking":` + tc.thinking + `}]}`
		r := httptest.NewRequest("PATCH", "http://localhost/api/v1/agents/code", bytes.NewBufferString(body))
		r.Header.Set("Idempotency-Key", store.ID())
		r.Header.Set("If-Match", "0")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("thinking %s: %d %s", tc.thinking, w.Code, w.Body)
		}
		if tc.status == 200 {
			var record model.Record
			_ = json.Unmarshal(w.Body.Bytes(), &record)
			var config map[string]any
			_ = json.Unmarshal(record.Data, &config)
			if config["chain"].([]any)[0].(map[string]any)["thinking"] == nil {
				t.Fatal("thinking setting lost")
			}
		}
	}
}
