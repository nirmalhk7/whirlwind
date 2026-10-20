package workflow

import (
	"context"
	"io"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func TestSkippedEntryRecordsFallbackBeforeSameProviderRetry(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "missing", Model: "m"}, model.AgentChoice{Provider: "fake", Model: "m"})
	calls := 0
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			calls++
			if calls == 1 {
				return provider.Output{}, io.EOF
			}
			return provider.Output{Text: "recovered"}, nil
		})
	}
	if _, err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	records, err := h.engine.Store.List(context.Background(), "sessions")
	if err != nil || len(records) != 2 {
		t.Fatalf("attempts=%d error=%v", len(records), err)
	}
	var count int
	err = h.engine.Store.DB.QueryRow("SELECT count(*) FROM attempt_events WHERE attempt_id=? AND kind='whirlwind/fallback'", records[0].ID).Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("fallback not attached to first execution after skip: count=%d error=%v", count, err)
	}
	err = h.engine.Store.DB.QueryRow("SELECT count(*) FROM attempt_events WHERE attempt_id=? AND kind='whirlwind/fallback'", records[1].ID).Scan(&count)
	if err != nil || count != 0 {
		t.Fatalf("same-provider retry mislabeled fallback: count=%d error=%v", count, err)
	}
}
