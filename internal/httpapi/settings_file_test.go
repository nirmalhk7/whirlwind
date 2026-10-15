package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/settingsfile"
	"whirlwind/internal/workflow"
)

const settingsYAMLName = "settings.yaml"

func TestSettingsYAMLSavesAndReflectsFileEdits(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), settingsYAMLName)
	_ = s.Store.Put(ctx, "settings", "global", workflow.DefaultSettings(), 0)
	_ = s.Store.Put(ctx, "providers", "fake", model.Provider{ID: "fake", Name: "Fake", Kind: "acp", Command: []string{"sh"}, Capabilities: model.AgentRoles()}, 0)
	if err := settingsfile.Attach(ctx, s.Store, file); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(file); err != nil {
		t.Fatalf("root settings file not created: %v", err)
	}
	send := func(method, path, body, version, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost/api/v1/"+path, strings.NewReader(body))
		r.Header.Set("If-Match", version)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	w := send("PATCH", "settings", `{"global_slots":7}`, "1", "workers")
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	contents, _ := os.ReadFile(file)
	if !strings.Contains(string(contents), "global_slots: 7") {
		t.Fatalf("UI settings not saved to YAML: %s", contents)
	}
	w = send("PATCH", "agents/code", `{"chain":[{"provider":"fake","model":"model-one","thinking":{"config_id":"reasoning","value":"high"}}]}`, "0", "agent")
	if w.Code != 200 {
		t.Fatalf("agent save: %d %s", w.Code, w.Body)
	}
	contents, _ = os.ReadFile(file)
	if !strings.Contains(string(contents), "config_id: reasoning") {
		t.Fatalf("thinking missing from YAML: %s", contents)
	}
	changed := strings.Replace(string(contents), "global_slots: 7", "global_slots: 6", 1)
	changed = strings.Replace(changed, "name: Fake", "name: File Provider", 1)
	changed = strings.Replace(changed, "model-one", "model-two", 1)
	if err := os.WriteFile(file, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	w = send("GET", "settings", "", "", "")
	var record model.Record
	_ = json.Unmarshal(w.Body.Bytes(), &record)
	var settings workflow.Settings
	_ = json.Unmarshal(record.Data, &settings)
	if w.Code != 200 || settings.GlobalSlots != 6 || record.Version != 3 {
		t.Fatalf("file not authoritative: %d %s", w.Code, w.Body)
	}
	w = send("PATCH", "settings", `{"global_slots":9}`, "2", "stale")
	if w.Code != 409 {
		t.Fatalf("stale UI overwrote file: %d %s", w.Code, w.Body)
	}
	w = send("GET", "providers/fake", "", "", "")
	if !strings.Contains(w.Body.String(), "File Provider") {
		t.Fatalf("provider edit ignored: %s", w.Body)
	}
	w = send("GET", "agents/code", "", "", "")
	if !strings.Contains(w.Body.String(), "model-two") {
		t.Fatalf("agent file edit ignored: %s", w.Body)
	}
	if err := settingsfile.Attach(ctx, s.Store, file); err != nil {
		t.Fatal(err)
	}
	w = send("GET", "settings", "", "", "")
	if !strings.Contains(w.Body.String(), `"global_slots":6`) {
		t.Fatal("file settings lost after reload")
	}
}

func TestSettingsYAMLInvalidEditsAndFailedReceiptsPreserveConfiguration(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), settingsYAMLName)
	_ = s.Store.Put(ctx, "settings", "global", workflow.DefaultSettings(), 0)
	if err := settingsfile.Attach(ctx, s.Store, file); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Store.DB.Exec("CREATE TRIGGER fail_settings_receipt BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT,'receipt failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("PATCH", "http://localhost/api/v1/settings", strings.NewReader(`{"global_slots":8}`))
	r.Header.Set("If-Match", "1")
	r.Header.Set("Idempotency-Key", "failure")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("failed save: %d %s", w.Code, w.Body)
	}
	after, _ := os.ReadFile(file)
	if string(before) != string(after) {
		t.Fatal("file changed despite failed receipt")
	}
	_ = os.WriteFile(file, []byte("workers: [invalid"), 0600)
	r = httptest.NewRequest("GET", "http://localhost/api/v1/settings", nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code < 400 {
		t.Fatalf("invalid file silently accepted: %d %s", w.Code, w.Body)
	}
}

func TestSettingsYAMLKeepsRuntimeStatusSeparateFromProviderConfiguration(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), settingsYAMLName)
	_ = s.Store.Put(ctx, "providers", "fake", model.Provider{ID: "fake", Name: "Configured name", Kind: "acp", Command: []string{"sh"}, Capabilities: []string{"code"}}, 0)
	if err := settingsfile.Attach(ctx, s.Store, file); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(file)
	record, _ := s.Store.Get(ctx, "providers", "fake")
	var value model.Provider
	_ = json.Unmarshal(record.Data, &value)
	value.Name = "Old attempt snapshot"
	value.Quota = "exhausted"
	if err := s.Store.Put(ctx, "providers", "fake", value, record.Version); err != nil {
		t.Fatal(err)
	}
	record, err := s.Store.Get(ctx, "providers", "fake")
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(record.Data, &value)
	if value.Name != "Configured name" || value.Quota != "exhausted" {
		t.Fatalf("file configuration or runtime status lost: %+v", value)
	}
	after, _ := os.ReadFile(file)
	if string(before) != string(after) {
		t.Fatal("runtime status rewrote YAML")
	}
}
