package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestParityPersistedHumanDecisionStopsAdapterAndReleasesAttempt(t *testing.T) {
	for _, kind := range []string{"question", "permission"} {
		t.Run(kind, func(t *testing.T) {
			assertPersistedHumanWaitStopsAttempt(t, kind)
		})
	}
}

func TestParityAnsweredQuestionResumesWithFreshAttemptDeadline(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "fixture-model"})
	ctx := context.Background()
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, _ provider.Input, hooks provider.Hooks) (provider.Output, error) {
			_, err := hooks.Questions(ctx, []model.Question{{ID: "storage", Text: "Which storage engine?"}})
			return provider.Output{}, err
		})
	}
	_, err := h.run(ctx)
	if !errors.Is(err, ErrHumanDecisionPending) {
		t.Fatal(err)
	}
	rows, err := h.engine.Store.List(ctx, "sessions")
	if err != nil || len(rows) != 1 {
		t.Fatalf("paused attempt missing: %v", err)
	}
	first, _ := store.Decode[model.Attempt](rows[0])
	questions, err := h.engine.Store.List(ctx, "questions")
	if err != nil || len(questions) != 1 {
		t.Fatalf("pending question missing: %v", err)
	}
	question, err := store.Decode[model.QuestionSet](questions[0])
	requireWorkflowSuccess(t, err)
	question.Status, question.Answers = "answered", map[string]string{"storage": "SQLite"}
	requireWorkflowSuccess(t, h.engine.Store.Put(ctx, "questions", question.ID, question, questions[0].Version))
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
			if !strings.Contains(input.Prompt, "SQLite") {
				t.Error("new attempt lost persisted human answer")
			}
			return provider.Output{Text: "Completed with clarified storage choice"}, nil
		})
	}
	out, err := h.run(ctx)
	requireWorkflowSuccess(t, err)
	if out.AttemptID == first.AttemptID {
		t.Fatal("human resume reused old ACP attempt")
	}
	row, err := h.engine.Store.Get(ctx, "sessions", out.AttemptID)
	requireWorkflowSuccess(t, err)
	second, err := store.Decode[model.Attempt](row)
	requireWorkflowSuccess(t, err)
	started, err := time.Parse(time.RFC3339Nano, second.StartedAt)
	requireWorkflowSuccess(t, err)
	deadline, err := time.Parse(time.RFC3339Nano, second.DeadlineAt)
	requireWorkflowSuccess(t, err)
	if deadline.Sub(started) != 300*time.Second || second.DeadlineAt <= first.DeadlineAt {
		t.Fatalf("new attempt did not receive normal fresh deadline: old=%s new=%s", first.DeadlineAt, second.DeadlineAt)
	}
	old, err := h.engine.Store.Get(ctx, "sessions", first.AttemptID)
	requireWorkflowSuccess(t, err)
	retained, _ := store.Decode[model.Attempt](old)
	if retained.DeadlineAt != first.DeadlineAt || retained.Status != "paused" || !retained.Finished {
		t.Fatal("resumption rewrote historical attempt deadline/status")
	}
}

func assertPersistedHumanWaitStopsAttempt(t *testing.T, kind string) {
	t.Helper()
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "fixture-model"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, _ provider.Input, hooks provider.Hooks) (provider.Output, error) {
			calls++
			if kind == "question" {
				_, _ = hooks.Questions(ctx, []model.Question{{ID: "choice", Text: "Choose a storage format"}})
			} else {
				_, _ = hooks.Permission(ctx, provider.Permission{Tool: "terminal/create", Command: []string{"not-allowed"}})
			}
			// Some ACP peers convert tool errors into model-visible responses. A
			// durable human wait must also revoke their active execution context.
			select {
			case <-ctx.Done():
				return provider.Output{}, ctx.Err()
			case <-time.After(2 * time.Second):
				return provider.Output{Text: "continued after pending human decision"}, nil
			}
		})
	}
	_, err := h.run(ctx)
	if !errors.Is(err, ErrHumanDecisionPending) || calls != 1 {
		t.Fatalf("persisted human wait did not stop attempt: calls=%d err=%v", calls, err)
	}
	rows, err := h.engine.Store.List(ctx, "sessions")
	requireWorkflowSuccess(t, err)
	if len(rows) != 1 {
		t.Fatalf("human wait created fallback attempts: %d", len(rows))
	}
	attempt, err := store.Decode[model.Attempt](rows[0])
	requireWorkflowSuccess(t, err)
	if !attempt.Finished || attempt.Status != "paused" {
		t.Fatalf("ACP attempt remained active during human wait: %+v", attempt)
	}
	started, err := time.Parse(time.RFC3339Nano, attempt.StartedAt)
	requireWorkflowSuccess(t, err)
	deadline, err := time.Parse(time.RFC3339Nano, attempt.DeadlineAt)
	requireWorkflowSuccess(t, err)
	if deadline.Sub(started) != 300*time.Second {
		t.Fatalf("human wait rewrote original hard deadline: %s", deadline.Sub(started))
	}
	h.engine.mu.Lock()
	active := h.engine.active[h.project.ID]
	h.engine.mu.Unlock()
	if active != 0 {
		t.Fatalf("human wait retained execution slot: %d", active)
	}
}
