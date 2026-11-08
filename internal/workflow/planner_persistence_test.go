package workflow

import (
	"context"
	"errors"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestMalformedPlannerOutputUsesBoundedRecoveryWithoutPersistenceConflict(t *testing.T) {
	e, task, _ := testEngine(t)
	e.retryWait = func(context.Context, time.Duration) error { return nil }
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			return provider.Output{Text: "{"}, nil
		})
	}

	err := e.Step(context.Background(), task.ID)
	if errors.Is(err, store.ErrPersistence) {
		t.Fatalf("malformed planner recovery became a persistence failure: %v", err)
	}
	if !errors.Is(err, ErrMalformedRoleOutput) {
		t.Fatalf("planner did not preserve malformed-output failure: %v", err)
	}
	attempts, listErr := e.Store.List(context.Background(), "sessions")
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(attempts) != 4 {
		t.Fatalf("malformed output recovery launched %d attempts, want initial launch plus three restarts", len(attempts))
	}
	stale, err := store.Decode[model.Attempt](attempts[0])
	if err != nil {
		t.Fatal(err)
	}
	staleWriter := &agentAttempt{agentRun: &agentRun{Engine: e, ctx: context.Background()}, id: stale.AttemptID, state: stale}
	if err := staleWriter.persistRetryEvent(map[string]string{"reason": "stale retry"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("superseded attempt recorded a retry event after its recovery episode advanced: %v", err)
	}
}
