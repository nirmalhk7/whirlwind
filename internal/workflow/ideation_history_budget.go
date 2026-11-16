package workflow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"whirlwind/internal/store"
)

type ideationHistoryBudgetKey struct{}

var errIdeationHistoryBudget = errors.New("ideation history budget exhausted")

func historyBudgetFitError(reconsiderationCount, reconsiderationBytes, historyBudget int) error {
	if reconsiderationBytes <= historyBudget {
		return nil
	}
	if reconsiderationCount == 0 {
		return fmt.Errorf("%w before comparison context", errIdeationHistoryBudget)
	}
	return fmt.Errorf("%w: cannot fit all pending human reconsideration requests", errIdeationHistoryBudget)
}

type ideationHistoryBudget struct {
	mu           sync.Mutex
	remaining    int
	perRetrieval int
	total        int
	runID        string
	persist      func(int) (int, error)
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
	return context.WithValue(ctx, ideationHistoryBudgetKey{}, &ideationHistoryBudget{remaining: total, total: total, perRetrieval: perRetrieval})
}

func (e *Engine) withDurableIdeationHistoryBudget(ctx context.Context, projectID string) (context.Context, error) {
	ctx = withIdeationHistoryBudget(ctx, e.settings(ctx).Ideation)
	runID := operationID(ctx)
	if runID == "" {
		return ctx, nil
	}
	budget := historyBudgetFrom(ctx)
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if budget.runID != "" {
		if budget.runID != runID {
			return ctx, store.ErrConflict
		}
		return ctx, nil
	}
	remaining, err := e.Store.InitializeIdeationHistoryBudget(ctx, runID, projectID, budget.total)
	if err != nil {
		return ctx, err
	}
	budget.remaining, budget.runID = remaining, runID
	budget.persist = func(amount int) (int, error) {
		remaining, err := e.Store.ConsumeIdeationHistory(ctx, runID, projectID, amount)
		if errors.Is(err, store.ErrHistoryBudget) {
			return 0, fmt.Errorf("%w: durable run allowance consumed", errIdeationHistoryBudget)
		}
		return remaining, err
	}
	return ctx, nil
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
	if budget.persist != nil {
		remaining, err := budget.persist(bytes)
		if err != nil {
			return err
		}
		budget.remaining = remaining
		return nil
	}
	budget.remaining -= bytes
	return nil
}
