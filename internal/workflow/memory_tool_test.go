package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func TestMemoryToolFindsUnexpectedLessonAndPersistsBudget(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	l := model.Lesson{ID: "unexpected", ProjectID: p.ID, Role: "code", Status: "tentative", Trigger: "contention", Mistake: "unbounded retries", Correction: "Use the contention sentinel", EvidenceIDs: []string{"historical"}}
	if err := e.Store.Put(ctx, "lessons", l.ID, l, 0); err != nil {
		t.Fatal(err)
	}
	calls := 0
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
			if h.SearchLessons == nil {
				t.Fatal("missing search tool")
			}
			result, err := h.SearchLessons(ctx, "contention")
			calls++
			assertMemorySearchResult(t, calls, result, err)
			return provider.Output{Text: "done"}, nil
		})
	}
	for range 3 {
		if _, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "unrelated task"}); err != nil {
			t.Fatal(err)
		}
	}
	records, _ := e.Store.List(ctx, "sessions")
	var found bool
	for _, r := range records {
		var a model.Attempt
		_ = json.Unmarshal(r.Data, &a)
		if a.Learning != nil && len(a.Learning.Searches) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("dynamic recall missing from evidence")
	}
}

func assertMemorySearchResult(t *testing.T, call int, result string, err error) {
	t.Helper()
	switch call {
	case 1:
		if err != nil || !strings.Contains(result, "contention sentinel") {
			t.Fatalf("unexpected problem recall: %s %v", result, err)
		}
	case 2:
		if strings.Contains(result, "contention sentinel") {
			t.Fatal("repeated lesson version delivered again")
		}
	case 3:
		if err == nil {
			t.Fatal("memory search allowance reset across attempts")
		}
	}
}
