package workflow

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestRetryBudgetsAndBackoff(t *testing.T) {
	for _, successAt := range []int{2, 3} {
		t.Run(string(rune('0'+successAt)), func(t *testing.T) {
			h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "m"})
			calls := 0
			var delays []time.Duration
			h.engine.retryWait = func(_ context.Context, d time.Duration) error { delays = append(delays, d); return nil }
			h.engine.Adapter = func(model.Provider) provider.Adapter {
				return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
					calls++
					if calls < successAt {
						return provider.Output{}, io.EOF
					}
					return provider.Output{Text: "done"}, nil
				})
			}
			_, err := h.run(context.Background())
			if err != nil || calls != successAt {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
			want := []time.Duration{2 * time.Second, 10 * time.Second}[:successAt-1]
			if !reflect.DeepEqual(delays, want) {
				t.Fatalf("delays=%v", delays)
			}
		})
	}
}

func TestExhaustedEntryPreservesWorkForNextModel(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "first"}, model.AgentChoice{Provider: "fake", Model: "second"})
	calls := []string{}
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			calls = append(calls, in.Model)
			path := filepath.Join(in.CWD, "partial.txt")
			if in.Model == "first" {
				writeRetryPartial(t, path)
				return provider.Output{SessionID: "first-session"}, io.EOF
			}
			if in.SessionID != "" {
				t.Errorf("session crossed models: %s", in.SessionID)
			}
			assertRetryPartial(t, path)
			return provider.Output{Text: "done"}, nil
		})
	}
	_, err := h.run(context.Background())
	if err != nil || !reflect.DeepEqual(calls, []string{"first", "first", "first", "second"}) {
		t.Fatalf("calls=%v error=%v", calls, err)
	}
	record, _ := h.engine.Store.Get(context.Background(), "providers", "fake")
	source, _ := store.Decode[model.Provider](record)
	if !source.Available || source.Quota == "exhausted" {
		t.Fatal("crash disabled provider globally")
	}
}

func TestFailureKindsChooseRetryOrSkip(t *testing.T) {
	cases := []struct {
		name    string
		failure error
		calls   int
		delay   time.Duration
	}{
		{"auth", errors.New("authentication required"), 1, 0},
		{"quota", errors.New("quota exhausted"), 1, 0},
		{"config", errors.New("invalid model"), 1, 0},
		{"rate", errors.New("rate limit exceeded"), 3, 10 * time.Second},
		{"service", errors.New("service unavailable"), 3, 2 * time.Second},
		{"unknown", errors.New("unclassified failure"), 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "m"})
			calls := 0
			var delays []time.Duration
			h.engine.retryWait = func(_ context.Context, d time.Duration) error { delays = append(delays, d); return nil }
			h.engine.Adapter = func(model.Provider) provider.Adapter {
				return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
					calls++
					return provider.Output{}, tc.failure
				})
			}
			_, err := h.run(context.Background())
			if err == nil || calls != tc.calls {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
			if tc.delay != 0 && (len(delays) != 2 || delays[0] != tc.delay) {
				t.Fatalf("delays=%v", delays)
			}
			if tc.delay == 0 && len(delays) != 0 {
				t.Fatal("blocked entry retried")
			}
		})
	}
}

func TestChainExhaustionHasOneAtomicRecoverySummary(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "one"}, model.AgentChoice{Provider: "fake", Model: "two"})
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			return provider.Output{}, io.EOF
		})
	}
	_, err := h.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "fake/two: 3 attempt") {
		t.Fatalf("missing cause: %v", err)
	}
	before, _ := h.engine.Store.List(context.Background(), "inbox")
	if len(before) != 0 {
		t.Fatal("intermediate notification")
	}
	h.engine.fail(context.Background(), h.task.ID, err)
	h.engine.fail(context.Background(), h.task.ID, err)
	notices, _ := h.engine.Store.List(context.Background(), "inbox")
	if len(notices) != 1 {
		t.Fatalf("notices=%d", len(notices))
	}
	notice, _ := store.Decode[model.Notification](notices[0])
	if !strings.Contains(notice.Message, "fake/one: 3 attempt") {
		t.Fatal("summary lost first entry")
	}
}

func writeRetryPartial(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("partial work"), 0600); err != nil {
		t.Fatal(err)
	}
}
func assertRetryPartial(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "partial work" {
		t.Errorf("partial work lost: %v", err)
	}
}
