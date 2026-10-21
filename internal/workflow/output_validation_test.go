package workflow

import (
	"context"
	"io"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func TestInvalidPlannerOutputGetsOneReadOnlyRepair(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	calls := 0
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			calls++
			if calls == 1 {
				return provider.Output{Text: `{"objective":"","acceptance_criteria":[]}`}, nil
			}
			if !in.ReadOnly || !strings.Contains(in.Prompt, "acceptance_criteria") {
				t.Fatal("repair lacks precise error or read-only restriction")
			}
			return provider.Output{Text: `{"objective":"Fix retry handling","acceptance_criteria":["Duplicate retries are safe"]}`}, nil
		})
	}
	out, err := e.agent(ctx, p, task, provider.Input{Role: "plan", CWD: cwd, Prompt: "plan retry handling", OutputContract: "plan"})
	if err != nil || calls != 2 || !strings.Contains(out.Text, "Duplicate retries") {
		t.Fatalf("repair: %+v %v calls=%d", out, err, calls)
	}
}

func TestInvalidOutputCannotTeachOrRepairForever(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	calls := 0
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			calls++
			return provider.Output{Text: `{"result":{"objective":""},"learning":{"lessons":[{"trigger":"retry","mistake":"unsafe write","correction":"check policy","paths":[".whirlwind.yaml"]}]}}`}, nil
		})
	}
	_, err := e.agent(ctx, p, task, provider.Input{Role: "plan", CWD: cwd, Prompt: "plan retry", OutputContract: "plan"})
	if err == nil || calls != 2 {
		t.Fatalf("unbounded/unchecked repair: %v calls=%d", err, calls)
	}
	lessons, _ := e.Store.List(ctx, "lessons")
	if len(lessons) != 0 {
		t.Fatal("invalid primary output taught a lesson")
	}
}

func TestTransientFailureDoesNotRenewOutputRepair(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	calls := 0
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			calls++
			if calls == 2 {
				return provider.Output{}, io.EOF
			}
			return provider.Output{Text: `{"objective":""}`}, nil
		})
	}
	_, err := e.agent(ctx, p, task, provider.Input{Role: "plan", CWD: cwd, Prompt: "plan retry", OutputContract: "plan"})
	if err == nil || calls != 3 {
		t.Fatalf("transport retry renewed format repair: calls=%d err=%v", calls, err)
	}
}
