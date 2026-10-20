package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/settingsfile"
)

func TestSettingsEditorUsesStartupPathAndSnapshotAndSavesWithReceipt(t *testing.T) {
	s := setup(t)
	path := filepath.Join(t.TempDir(), "launched-with.yaml")
	original := "# Startup\nversion: 1\nworkers: {global_slots: 4}\nproviders: []\nagents: {}\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := settingsfile.Attach(context.Background(), s.Store, path); err != nil {
		t.Fatal(err)
	}
	send := func(method, suffix, key string, body any) *httptest.ResponseRecorder {
		encoded, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "http://localhost/api/v1/settings/file"+suffix, strings.NewReader(string(encoded)))
		req.Header.Set(idempotencyKeyHeader, key)
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, req)
		return response
	}
	changed := "# External edit\n" + original
	_ = os.WriteFile(path, []byte(changed), 0600)
	current := readSettingsFileViews(t, send, path, original, changed)
	next := "# Saved formatting\nversion: 1\nworkers: {global_slots: 7}\nproviders: []\nagents: {}\n"
	assertSettingsSaveReplay(t, send, path, original, changed, next, current.Revision)
	assertSettingsSaveRollback(t, s, send, path, changed, next)
}

func readSettingsFileViews(t *testing.T, send func(string, string, string, any) *httptest.ResponseRecorder, path, original, changed string) settingsfile.FileView {
	t.Helper()
	response := send("GET", "", "", nil)
	var snapshot settingsfile.FileView
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); response.Code != 200 || err != nil || snapshot.Path != path || snapshot.Content != original {
		t.Fatalf("snapshot: %d %s %v", response.Code, response.Body, err)
	}
	response = send("GET", "?view=file", "", nil)
	var current settingsfile.FileView
	if err := json.Unmarshal(response.Body.Bytes(), &current); response.Code != 200 || err != nil || current.Content != changed {
		t.Fatalf("file: %d %s %v", response.Code, response.Body, err)
	}
	return current
}

func assertSettingsSaveReplay(t *testing.T, send func(string, string, string, any) *httptest.ResponseRecorder, path, original, changed, next, revision string) {
	t.Helper()
	input := map[string]string{"content": next, "revision": revision}
	response := send("PATCH", "", "save-yaml", input)
	if response.Code != 200 {
		t.Fatalf("save: %d %s", response.Code, response.Body)
	}
	actual, _ := os.ReadFile(path)
	if string(actual) != next {
		t.Fatal("file not saved exactly")
	}
	// A retry must replay the successful receipt even after another external edit.
	_ = os.WriteFile(path, []byte(changed), 0600)
	replay := send("PATCH", "", "save-yaml", input)
	actual, _ = os.ReadFile(path)
	if replay.Code != 200 || string(actual) != changed {
		t.Fatalf("replay overwrote disk: %d %s", replay.Code, replay.Body)
	}
	stale := send("PATCH", "", "stale-yaml", map[string]string{"content": original, "revision": "stale"})
	if stale.Code != 409 {
		t.Fatalf("stale: %d %s", stale.Code, stale.Body)
	}
	invalid := send("PATCH", "", "invalid-yaml", map[string]string{"content": "workers: [invalid", "revision": revision})
	if invalid.Code != 422 {
		t.Fatalf("invalid: %d %s", invalid.Code, invalid.Body)
	}
	response = send("GET", "", "", nil)
	var snapshot settingsfile.FileView
	_ = json.Unmarshal(response.Body.Bytes(), &snapshot)
	if snapshot.Content != original {
		t.Fatal("startup snapshot changed after save")
	}
}

func assertSettingsSaveRollback(t *testing.T, s *Server, send func(string, string, string, any) *httptest.ResponseRecorder, path, changed, next string) {
	t.Helper()
	_, err := s.Store.DB.Exec("CREATE TRIGGER fail_editor_receipt BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT,'receipt failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	response := send("GET", "?view=file", "", nil)
	var current settingsfile.FileView
	_ = json.Unmarshal(response.Body.Bytes(), &current)
	failed := send("PATCH", "", "failed-yaml", map[string]string{"content": next, "revision": current.Revision})
	actual, _ := os.ReadFile(path)
	if failed.Code != 503 || string(actual) != changed {
		t.Fatalf("failed receipt changed file: %d %s", failed.Code, failed.Body)
	}
}
