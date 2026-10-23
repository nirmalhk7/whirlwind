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
	"whirlwind/internal/store"
)

const (
	runnerStopURL        = "http://localhost/api/v1/runners/stop"
	testSettingsFilename = "settings.yaml"
	resumeAllKey         = "resume-all"
)

func TestStopAllRunnersPausesUnfinishedTasksAndCanReplay(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	for _, stage := range []string{"planning", "coding", "questions", "approval", "paused", "complete", "pr_created"} {
		if err := s.Store.Put(ctx, "tasks", stage, model.Task{ID: stage, Stage: stage, ResumeStage: "review"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", runnerStopURL, strings.NewReader(`{}`))
		req.Header.Set(idempotencyKeyHeader, "stop-all")
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, req)
		return response
	}
	response := send()
	assertPausedRunnerCount(t, response, 4)
	assertStoppedTaskStages(t, s.Store, ctx)
	replay := send()
	if replay.Code != 200 || replay.Body.String() != response.Body.String() {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
}

func assertPausedRunnerCount(t *testing.T, response *httptest.ResponseRecorder, count int) {
	t.Helper()
	if response.Code != 200 {
		t.Fatalf("%d %s", response.Code, response.Body)
	}
	var result struct {
		Paused int `json:"paused"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Paused != count {
		t.Fatalf("result: %+v %v", result, err)
	}
}

func assertStoppedTaskStages(t *testing.T, db *store.Store, ctx context.Context) {
	t.Helper()
	for _, stage := range []string{"planning", "coding", "questions", "approval", "paused", "complete", "pr_created"} {
		record, err := db.Get(ctx, "tasks", stage)
		if err != nil {
			t.Fatal(err)
		}
		task, _ := store.Decode[model.Task](record)
		if stage == "paused" || stage == "complete" || stage == "pr_created" {
			if task.Stage != stage || record.Version != 1 {
				t.Fatalf("changed %s: %+v", stage, task)
			}
		} else if task.Stage != "paused" || task.ResumeStage != stage {
			t.Fatalf("not resumable: %+v", task)
		}
	}
}

func TestKillButtonPersistsStoppedFlagAndPreservesStartupSnapshot(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), testSettingsFilename)
	if err := settingsfile.Attach(ctx, s.Store, file); err != nil {
		t.Fatal(err)
	}
	startup := s.Store.Source.(*settingsfile.Source).Snapshot()
	send := func(key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", runnerStopURL, strings.NewReader(`{}`))
		req.Header.Set(idempotencyKeyHeader, key)
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, req)
		return response
	}
	for _, key := range []string{"kill", "kill", "kill-again"} {
		response := send(key)
		if response.Code != 200 {
			t.Fatalf("kill: %d %s", response.Code, response.Body)
		}
		raw, err := os.ReadFile(file)
		if err != nil || !strings.Contains(string(raw), "stopped: true") {
			t.Fatalf("stop flag missing: %s %v", raw, err)
		}
	}
	if s.Store.Source.(*settingsfile.Source).Snapshot() != startup {
		t.Fatal("kill changed startup snapshot")
	}
	if err := settingsfile.Attach(ctx, s.Store, file); err != nil {
		t.Fatal(err)
	}
	record, err := s.Store.Get(ctx, "settings", "global")
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	_ = json.Unmarshal(record.Data, &value)
	if value["stopped"] != true {
		t.Fatalf("restart lost stop flag: %s", record.Data)
	}
}

func TestKillReceiptFailureDoesNotPersistFlagOrPauseTasks(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), testSettingsFilename)
	if err := settingsfile.Attach(ctx, s.Store, file); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(file)
	if err := s.Store.Put(ctx, "tasks", "active", model.Task{ID: "active", Stage: "coding"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec("CREATE TRIGGER fail_kill_receipt BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT,'receipt failure'); END"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", runnerStopURL, strings.NewReader(`{}`))
	req.Header.Set(idempotencyKeyHeader, "failed-kill")
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, req)
	after, _ := os.ReadFile(file)
	record, _ := s.Store.Get(ctx, "tasks", "active")
	task, _ := store.Decode[model.Task](record)
	if response.Code != 503 || string(before) != string(after) || task.Stage != "coding" {
		t.Fatalf("non-atomic kill: %d task=%+v file=%s", response.Code, task, after)
	}
}

func TestResumeAllRunnersPersistsFlagAndRestoresOnlyGloballyStoppedTasks(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), testSettingsFilename)
	if err := settingsfile.Attach(ctx, s.Store, file); err != nil {
		t.Fatal(err)
	}
	for _, task := range []model.Task{
		{ID: "active", Stage: "coding"}, {ID: "question", Stage: "questions"}, {ID: "manual", Stage: "paused", ResumeStage: "review", Error: "Paused by user"}, {ID: "done", Stage: "complete"},
	} {
		task.Selection = &model.SelectionAuthorization{CandidateID: task.ID, SelectedAt: "2026-10-22T00:00:00Z"}
		if err := s.Store.Put(ctx, "tasks", task.ID, task, 0); err != nil {
			t.Fatal(err)
		}
	}
	send := func(action, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://localhost/api/v1/runners/"+action, strings.NewReader(`{}`))
		req.Header.Set(idempotencyKeyHeader, key)
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, req)
		return response
	}
	if response := send("stop", "stop-before-resume"); response.Code != 200 {
		t.Fatalf("stop: %d %s", response.Code, response.Body)
	}
	response := send("resume", resumeAllKey)
	if response.Code != 200 {
		t.Fatalf("resume: %d %s", response.Code, response.Body)
	}
	assertRunnerFileFlag(t, file, "stopped: false")
	assertResumedTaskStages(t, s.Store, ctx)
	assertResumeReplayPreservesLaterStop(t, send, response, file)
}

func assertResumedTaskStages(t *testing.T, db *store.Store, ctx context.Context) {
	t.Helper()
	for id, want := range map[string]string{"active": "coding", "question": "questions", "manual": "paused", "done": "complete"} {
		record, err := db.Get(ctx, "tasks", id)
		if err != nil {
			t.Fatal(err)
		}
		task, _ := store.Decode[model.Task](record)
		if task.Stage != want {
			t.Fatalf("%s stage=%s want=%s", id, task.Stage, want)
		}
	}
}

func assertResumeReplayPreservesLaterStop(t *testing.T, send func(string, string) *httptest.ResponseRecorder, response *httptest.ResponseRecorder, file string) {
	t.Helper()
	replay := send("resume", resumeAllKey)
	if replay.Code != 200 || replay.Body.String() != response.Body.String() {
		t.Fatalf("resume replay: %d %s", replay.Code, replay.Body)
	}
	// A stale retry of resume must not clear a subsequent stop.
	if response := send("stop", "stop-again"); response.Code != 200 {
		t.Fatal(response.Body)
	}
	if response := send("resume", resumeAllKey); response.Code != 200 {
		t.Fatal(response.Body)
	}
	assertRunnerFileFlag(t, file, "stopped: true")
}

func assertRunnerFileFlag(t *testing.T, path, flag string) {
	t.Helper()
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), flag) {
		t.Fatalf("settings flag %q not found: %s", flag, raw)
	}
}
