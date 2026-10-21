package workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

const semanticMemoryQuery = "retries duplicated records"

func TestSemanticMemoryRequiresSuccessfulEvaluationAndCachesLessons(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	calls := 0
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		data := []any{}
		for i := range request.Input {
			data = append(data, map[string]any{"index": i, "embedding": []float64{1, 0}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer endpoint.Close()
	p.Policy.Embeddings = &model.Embeddings{Endpoint: endpoint.URL, Model: "test", LessonsEnabled: true}
	l := model.Lesson{ID: "idempotency", ProjectID: p.ID, Role: "code", Status: "tentative", Trigger: "idempotency", Mistake: "unsafe writes", Correction: "Use an idempotency key", EvidenceIDs: []string{"old-evidence"}}
	if err := e.Store.Put(ctx, "lessons", l.ID, l, 0); err != nil {
		t.Fatal(err)
	}
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			if calls == 0 && strings.Contains(in.Prompt, "Use an idempotency key") {
				t.Fatal("unvalidated semantic memory enabled")
			}
			return provider.Output{Text: "done"}, nil
		})
	}
	input := provider.Input{Role: "code", CWD: cwd, Prompt: semanticMemoryQuery}
	task.Issue.Title = semanticMemoryQuery
	task.Issue.Body = ""
	if _, err := e.agent(ctx, p, task, input); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("unvalidated recall called embedding service")
	}
	result, err := e.EvaluateLessonSearch(ctx, p, []LessonFixture{{Role: "code", Query: semanticMemoryQuery, Relevant: []string{l.ID}}})
	if err != nil || !result.Enabled {
		t.Fatalf("semantic evaluation: %+v %v", result, err)
	}
	callsBefore := calls
	run := &agentRun{Engine: e, ctx: ctx, project: p, task: task, input: input}
	block, refs, err := run.lessonBlock(ctx, semanticMemoryQuery, nil)
	if err != nil || len(refs) != 1 || !strings.Contains(block, "idempotency key") {
		t.Fatalf("semantic recall: %s %v", block, err)
	}
	if calls-callsBefore > 1 {
		t.Fatal("unchanged lesson embedded again")
	}
}
