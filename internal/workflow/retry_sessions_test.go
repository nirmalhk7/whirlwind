package workflow

import (
	"context"
	"errors"
	"io"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestFailedResumeKeepsSavedSession(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "m"})
	h.task.Recoveries = 2
	saved := model.Attempt{ElapsedMS: fixtureRuntime(0), ExecutionProfile: h.executionProfile(t), Task: h.task.ID, Role: "code", Provider: "fake", CWD: h.cwd, PlanVersion: 1, Model: "m", Transport: "acp", SessionID: "saved"}
	if err := h.engine.Store.Put(context.Background(), "sessions", "original", saved, 0); err != nil {
		t.Fatal(err)
	}
	calls := 0
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			calls++
			if in.SessionID != "saved" {
				t.Errorf("resume %d used %q", calls, in.SessionID)
			}
			return provider.Output{}, io.EOF
		})
	}
	_, err := h.run(context.Background())
	if err == nil || calls != 4 {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
	records, _ := h.engine.Store.List(context.Background(), "sessions")
	for _, record := range records {
		state, _ := store.Decode[model.Attempt](record)
		if state.SessionID != "saved" {
			t.Fatal("failed load erased session")
		}
	}
}

func TestUnitSessionDoesNotCrossScope(t *testing.T) {
	h := newRetryHarness(t)
	ctx := context.Background()
	for _, unit := range []string{"wanted", "other"} {
		saved := model.Attempt{ElapsedMS: fixtureRuntime(0), ExecutionProfile: h.executionProfile(t), Task: h.task.ID, UnitID: unit, Role: "code", Provider: "fake", CWD: h.cwd, PlanVersion: 1, Model: "fixture-model", Transport: "acp", SessionID: unit}
		if err := h.engine.Store.Put(ctx, "sessions", store.ID(), saved, 0); err != nil {
			t.Fatal(err)
		}
	}
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			if in.SessionID != "wanted" {
				t.Errorf("crossed unit: %q", in.SessionID)
			}
			return provider.Output{Text: "done"}, nil
		})
	}
	_, err := h.engine.agent(ctx, h.project, h.task, provider.Input{Role: "code", CWD: h.cwd, UnitID: "wanted"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSessionLookupFailureStopsExecution(t *testing.T) {
	h := newRetryHarness(t)
	if err := h.engine.Store.Put(context.Background(), "sessions", "malformed", map[string]any{"plan_version": "wrong type"}, 0); err != nil {
		t.Fatal(err)
	}
	calls := 0
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			calls++
			return provider.Output{}, nil
		})
	}
	_, err := h.run(context.Background())
	if !errors.Is(err, store.ErrPersistence) || calls != 0 {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
}

func TestRepeatedRunPreservesExhaustedCrashBudget(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "m"})
	calls := 0
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			calls++
			return provider.Output{}, io.EOF
		})
	}
	for range 2 {
		if _, err := h.run(context.Background()); err == nil {
			t.Fatal("failure lost")
		}
	}
	if calls != 4 {
		t.Fatalf("repeated run reset the persisted initial-plus-three restart budget: %d", calls)
	}
}
