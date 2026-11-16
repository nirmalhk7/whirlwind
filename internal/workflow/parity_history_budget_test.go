package workflow

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const (
	historyFixtureRunID = "history-run"
)

func TestParityIdeationHistoryBudgetSurvivesDatabaseReopen(t *testing.T) {
	e, _, _ := testEngine(t)
	ctx := context.Background()
	settings := e.settings(ctx)
	settings.Ideation.HistoryTokenBudget, settings.Ideation.HistoryRunTokenBudget = 1000, 1000
	r, _ := e.Store.Get(ctx, "settings", "global")
	if err := e.Store.Put(ctx, "settings", "global", settings, r.Version); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(ctx, "operations", historyFixtureRunID, model.Operation{Kind: "ideation", Subject: "p", Stage: "running"}, 0); err != nil {
		t.Fatal(err)
	}
	first := withIdeationHistoryBudget(WithOperation(ctx, historyFixtureRunID), settings.Ideation)
	first, err := e.withDurableIdeationHistoryBudget(first, "p")
	if err != nil {
		t.Fatal(err)
	}
	if err := historyBudgetFrom(first).consume(400); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(filepath.Join(e.Root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	replacement := New(reopened, e.GitHub, e.Root)
	resumed := withIdeationHistoryBudget(WithOperation(ctx, historyFixtureRunID), settings.Ideation)
	resumed, err = replacement.withDurableIdeationHistoryBudget(resumed, "p")
	if err != nil {
		t.Fatal(err)
	}
	if err := historyBudgetFrom(resumed).consume(601); err == nil || !strings.Contains(err.Error(), "history budget exhausted") {
		t.Fatalf("restart reset consumed run history allowance: %v", err)
	}
	var limit, used int
	if err := reopened.DB.QueryRow("SELECT history_token_limit,history_tokens_used FROM ideation_runs WHERE run_id='history-run'").Scan(&limit, &used); err != nil || limit != 1000 || used != 400 {
		t.Fatalf("history accounting not durable/bounded: limit=%d used=%d err=%v", limit, used, err)
	}
}
