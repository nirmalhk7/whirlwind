package workflow

import (
	"context"
	"errors"
	"strings"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const ideationPrefix = "ideation:"

func (e *Engine) Ideate(ctx context.Context, p model.Project, day string) (resultErr error) {
	ctx, release, err := e.projectRunnerContext(ctx, p.ID)
	if err != nil {
		return err
	}
	defer release()
	key := "generation:" + p.ID
	lease, err := e.Store.Claim(ctx, key, e.owner, time.Minute)
	if err != nil {
		return err
	}
	defer func() { _ = e.Store.Release(context.Background(), key, e.owner, lease.Fence) }()
	ctx = store.WithLeaseFence(ctx, key, lease)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ctx = withIdeationHistoryBudget(ctx, e.settings(ctx).Ideation)
	// Recheck after acquiring the lease: a scheduled project may have been unregistered.
	if _, err := e.Project(ctx, p.ID); err != nil {
		return err
	}
	ctx, finish, err := e.ensureIdeationOperation(ctx, p, day)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, finish(resultErr)) }()
	e.renewLease(ctx, cancel, key, lease.Fence, nil)
	if err := e.pinIdeationRunIdentity(ctx, p); err != nil {
		return err
	}
	ctx, err = e.withDurableIdeationHistoryBudget(ctx, p.ID)
	if err != nil {
		return err
	}
	if err := e.generateAndSaveIdeation(ctx, p, day); err != nil {
		return err
	}
	return e.resolveProjectNotices(ctx, p, "ideation")
}

func (e *Engine) IdeateWhenAvailable(ctx context.Context, p model.Project, day string) error {
	ctx, release, err := e.projectRunnerContext(ctx, p.ID)
	if err != nil {
		return err
	}
	defer release()
	for {
		err := e.Ideate(ctx, p, day)
		if !errors.Is(err, store.ErrLeased) {
			return err
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (e *Engine) previousDirections(ctx context.Context, project string, terms ...string) ([]model.Direction, error) {
	historyBudget, _ := discoveryHistoryBudgets(e.settings(ctx).Ideation)
	return e.searchPreviousDirections(ctx, project, strings.Join(terms, " "), historyBudget)
}

func discoveryHistoryBudgets(settings IdeationSchedule) (int, int) {
	perRequest := settings.HistoryTokenBudget
	if perRequest <= 0 {
		perRequest = 4000
	}
	runBudget := settings.HistoryRunTokenBudget
	if runBudget <= 0 {
		runBudget = 12000
	}
	attempts := settings.NoveltyRepairAttempts
	if attempts <= 0 {
		attempts = 3
	}
	perCallBudget := runBudget / (attempts * 2)
	if perCallBudget <= 0 {
		perCallBudget = 1
	}
	if perRequest > perCallBudget {
		perRequest = perCallBudget
	}
	planBudget := perRequest / 4
	if planBudget == 0 && perRequest > 1 {
		planBudget = 1
	}
	historyBudget := perRequest - planBudget
	return historyBudget, planBudget
}

func (e *Engine) searchPreviousDirections(ctx context.Context, project, terms string, perRequest int) ([]model.Direction, error) {
	settings := e.settings(ctx).Ideation
	limit := settings.HistoryRecordLimit
	if limit <= 0 || limit > 12 {
		limit = 12
	}
	query := model.ProposalQuery{ProjectID: project, Limit: limit}
	if strings.TrimSpace(terms) != "" {
		query.Terms = terms
	}
	proposals, err := e.Store.SearchProposals(ctx, query)
	if err != nil {
		return nil, err
	}
	previous := make([]model.Direction, 0, len(proposals))
	for _, proposal := range proposals {
		previous = append(previous, proposalHistoryDirection(proposal))
	}
	return boundProposalHistory(previous, perRequest, limit), nil
}

func boundProposalHistory(history []model.Direction, maxBytes, maxRecords int) []model.Direction {
	if maxRecords <= 0 || maxRecords > 12 {
		maxRecords = 12
	}
	if maxBytes < 0 {
		maxBytes = 0
	}
	bounded := make([]model.Direction, 0, min(len(history), maxRecords))
	for _, proposal := range history {
		if len(bounded) >= maxRecords {
			break
		}
		candidate := append(append([]model.Direction{}, bounded...), proposal)
		if len(jsonText(compactProposalHistory(candidate))) > maxBytes {
			continue
		}
		bounded = append(bounded, proposal)
	}
	return bounded
}
