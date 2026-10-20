package workflow

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestCrashRetriesResumeSessionUntilSuccess(t *testing.T) {
	e, task, cwd := testEngine(t)
	e.retryWait = func(context.Context, time.Duration) error { return nil }
	ctx := context.Background()
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, hooks provider.Hooks) (provider.Output, error) {
			calls++
			if calls > 1 && in.SessionID != "saved-before-crash" {
				t.Errorf("lost saved session: %q", in.SessionID)
			}
			hooks.Event("whirlwind/session", map[string]any{"session_id": "saved-before-crash"})
			if calls < 3 {
				return provider.Output{}, io.ErrUnexpectedEOF
			}
			return provider.Output{Text: "recovered"}, nil
		})
	}
	out, err := e.agent(ctx, project, task, provider.Input{Role: "code", CWD: cwd, Prompt: "finish"})
	if err != nil || out.Text != "recovered" || calls != 3 {
		t.Fatalf("calls=%d output=%+v error=%v", calls, out, err)
	}
	assertRetryAttemptLinks(t, e)
}

func TestFailureCreatesOneRecoveryNotice(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	e.fail(ctx, task.ID, errors.New("chain exhausted"))
	e.fail(ctx, task.ID, errors.New("second failure"))
	notices, err := e.Store.List(ctx, "inbox")
	if err != nil || len(notices) != 1 {
		t.Fatalf("notices=%d error=%v", len(notices), err)
	}
}

func TestResumeIgnoresNewerEmptySession(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	for _, session := range []string{"usable", ""} {
		value := model.Attempt{Task: task.ID, Role: "code", Provider: "fake", CWD: cwd, PlanVersion: 1, SessionID: session}
		if err := e.Store.Put(ctx, "sessions", store.ID(), value, 0); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := e.resumeSession(ctx, task.ID, "code", "fake", cwd, 1); err != nil || got != "usable" {
		t.Fatalf("session=%q", got)
	}
}

func assertRetryAttemptLinks(t *testing.T, e *Engine) {
	t.Helper()
	ctx := context.Background()
	records, err := e.Store.List(ctx, "sessions")
	if err != nil || len(records) != 3 {
		t.Fatalf("attempts=%d error=%v", len(records), err)
	}
	for i, record := range records {
		state, decodeErr := store.Decode[model.Attempt](record)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if i > 0 && state.PreviousAttemptID != records[i-1].ID {
			t.Fatal("attempt predecessor lost")
		}
	}
}
