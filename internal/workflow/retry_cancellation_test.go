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

func TestCancellationDuringBackoffStopsChain(t *testing.T) {
	for _, cancelAt := range []int{1, 2} {
		t.Run(string(rune('0'+cancelAt)), func(t *testing.T) {
			h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "first"}, model.AgentChoice{Provider: "fake", Model: "next"})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, waits := 0, 0
			h.engine.retryWait = func(ctx context.Context, _ time.Duration) error {
				waits++
				if waits == cancelAt {
					cancel()
				}
				return ctx.Err()
			}
			h.engine.Adapter = func(model.Provider) provider.Adapter {
				return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
					calls++
					return provider.Output{}, io.EOF
				})
			}
			_, err := h.run(ctx)
			if !errors.Is(err, context.Canceled) || calls != cancelAt {
				t.Fatalf(retryFailureFormat, calls, err)
			}
		})
	}
}

func TestCancellationDuringExecutionNeverFallsBack(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "first"}, model.AgentChoice{Provider: "fake", Model: "next"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			calls++
			cancel()
			return provider.Output{}, io.EOF
		})
	}
	_, err := h.run(ctx)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf(retryFailureFormat, calls, err)
	}
}

func TestPersistenceFailureDominatesCrash(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "m"})
	_, err := h.engine.Store.DB.Exec("CREATE TRIGGER reject_metric BEFORE INSERT ON resources WHEN NEW.kind='metrics' BEGIN SELECT RAISE(ABORT,'metric failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			calls++
			return provider.Output{}, io.EOF
		})
	}
	_, err = h.run(context.Background())
	if !errors.Is(err, store.ErrPersistence) || calls != 1 {
		t.Fatalf(retryFailureFormat, calls, err)
	}
}

func TestRunTimeoutDoesNotResetAcrossRetries(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "first"}, model.AgentChoice{Provider: "fake", Model: "next"})
	h.project.Policy.RunTimeoutSeconds = 1
	h.engine.retryWait = func(ctx context.Context, _ time.Duration) error { <-ctx.Done(); return ctx.Err() }
	calls := 0
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			calls++
			return provider.Output{}, io.EOF
		})
	}
	_, err := h.run(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf(retryFailureFormat, calls, err)
	}
}

func TestChildTransportCancellationRetriesWithoutCancellingTask(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "m"})
	calls := 0
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			calls++
			if calls == 1 {
				return provider.Output{}, &provider.TransportError{Cause: context.Canceled}
			}
			return provider.Output{Text: "recovered"}, nil
		})
	}
	out, err := h.run(context.Background())
	if err != nil || out.Text != "recovered" || calls != 2 {
		t.Fatalf("child disconnect stopped live task: calls=%d error=%v", calls, err)
	}
}
