package workflow

import (
	"context"
	"errors"
	"sync"
)

type ideationHistoryBudgetKey struct{}

type ideationHistoryBudget struct {
	mu           sync.Mutex
	remaining    int
	perRetrieval int
}

func withIdeationHistoryBudget(ctx context.Context, settings IdeationSchedule) context.Context {
	if current, _ := ctx.Value(ideationHistoryBudgetKey{}).(*ideationHistoryBudget); current != nil {
		return ctx
	}
	perRetrieval := settings.HistoryTokenBudget
	if perRetrieval <= 0 {
		perRetrieval = 4000
	}
	total := settings.HistoryRunTokenBudget
	if total <= 0 {
		total = 12000
	}
	return context.WithValue(ctx, ideationHistoryBudgetKey{}, &ideationHistoryBudget{remaining: total, perRetrieval: perRetrieval})
}

func historyBudgetFrom(ctx context.Context) *ideationHistoryBudget {
	budget, _ := ctx.Value(ideationHistoryBudgetKey{}).(*ideationHistoryBudget)
	return budget
}

func (budget *ideationHistoryBudget) requestLimit() int {
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if budget.remaining < budget.perRetrieval {
		return budget.remaining
	}
	return budget.perRetrieval
}

func (budget *ideationHistoryBudget) consume(bytes int) error {
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if bytes < 0 || bytes > budget.remaining {
		return errors.New("ideation history budget exhausted during bounded retrieval")
	}
	budget.remaining -= bytes
	return nil
}
