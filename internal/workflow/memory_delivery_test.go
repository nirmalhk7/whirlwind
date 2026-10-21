package workflow

import (
	"context"
	"strings"
	"sync"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestAutomaticMemoryDoesNotRepeatInResumedOperation(t *testing.T) {
	e, task, cwd := testEngine(t)
	task.Issue.Title = "fix retries"
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	lesson := model.Lesson{ID: "retry", ProjectID: p.ID, Role: "code", Status: "tentative", Trigger: "retries", Mistake: "duplicate writes", Correction: "Use sentinel delivery once", EvidenceIDs: []string{"historical"}}
	if err := e.Store.Put(ctx, "lessons", lesson.ID, lesson, 0); err != nil {
		t.Fatal(err)
	}
	calls := 0
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			calls++
			if calls == 1 && !strings.Contains(in.Prompt, "sentinel delivery once") {
				t.Fatal("first automatic recall absent")
			}
			if calls == 2 && strings.Contains(in.Prompt, "sentinel delivery once") {
				t.Fatal("automatic memory duplicated in resumed operation")
			}
			return provider.Output{Text: "done", SessionID: "native-session"}, nil
		})
	}
	for range 2 {
		if _, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "fix retries"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFailedSearchStillReportsConsumedAllowance(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, _ provider.Input, h provider.Hooks) (provider.Output, error) {
			if _, err := e.Store.DB.Exec("ALTER TABLE lesson_search RENAME TO unavailable_lesson_search"); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := h.SearchLessons(ctx, "unexpected schema error"); err == nil {
					t.Fatal("broken search unexpectedly worked")
				}
			}
			return provider.Output{Text: "done"}, nil
		})
	}
	out, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "fix issue"})
	if err != nil {
		t.Fatal(err)
	}
	record, err := e.Store.Get(ctx, "sessions", out.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := store.Decode[model.Attempt](record)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Learning.SearchCalls != 2 || attempt.Learning.SearchBytes != 0 {
		t.Fatalf("failed calls disappeared: %+v", attempt.Learning)
	}
}

func TestMemorySearchCanOverlapRuntimeEvidence(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, _ provider.Input, h provider.Hooks) (provider.Output, error) {
			var pending sync.WaitGroup
			pending.Add(2)
			go func() {
				defer pending.Done()
				for range 30 {
					h.Event("whirlwind/context", model.ContextUsage{Used: 10, Size: 100, Source: "fixture"})
				}
			}()
			go func() {
				defer pending.Done()
				for range 2 {
					if _, err := h.SearchLessons(ctx, "unexpected schema error"); err != nil {
						t.Error(err)
					}
				}
			}()
			pending.Wait()
			return provider.Output{Text: "done"}, nil
		})
	}
	if _, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "fix issue"}); err != nil {
		t.Fatal(err)
	}
}
