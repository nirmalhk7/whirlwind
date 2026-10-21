package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func (a *agentRun) executeChoice(source model.Provider, choice model.AgentChoice) (provider.Output, bool, error) {
	a.input.Model, a.input.Thinking = choice.Model, choice.Thinking
	for attempt := 1; attempt <= 3; attempt++ {
		output, kind, execution, err := a.runChoiceAttempt(source)
		if kind == provider.FailureUnknown {
			return output, false, err
		}
		if kind != provider.FailureTransient && kind != provider.FailureRateLimit || attempt == 3 {
			return output, true, fmt.Errorf("%s/%s: %d attempt(s): %w", source.ID, choice.Model, attempt, err)
		}
		if err := execution.waitRetry(attempt, kind, err); err != nil {
			return output, false, err
		}
	}
	panic("unreachable attempt budget")
}

func (a *agentRun) runChoiceAttempt(source model.Provider) (provider.Output, provider.FailureKind, *agentAttempt, error) {
	output, kind, execution, err := a.retryRecovery(source)
	var invalid *outputValidationError
	if errors.As(err, &invalid) && a.input.OutputContract != "directions" && !a.outputRepairUsed {
		a.outputRepairUsed = true
		a.repairOutput(err)
		return a.retryRecovery(source)
	}
	return output, kind, execution, err
}

func (a *agentRun) runSingleAttempt(source model.Provider) (provider.Output, provider.FailureKind, *agentAttempt, error) {
	if err := a.ctx.Err(); err != nil {
		return provider.Output{}, provider.FailureUnknown, nil, err
	}
	a.input.SessionID = ""
	execution, err := a.newAttempt(source)
	if err != nil {
		return provider.Output{}, provider.FailureUnknown, nil, err
	}
	output, err := execution.execute()
	kind, failure := execution.failureKind(err)
	if failure != nil {
		return output, provider.FailureUnknown, nil, failure
	}
	return output, kind, execution, err
}

func (a *agentAttempt) failureKind(err error) (provider.FailureKind, error) {
	if terminal := terminalExecutionError(a.ctx, err); terminal != nil {
		return provider.FailureUnknown, terminal
	}
	if err == nil {
		return provider.FailureUnknown, nil
	}
	latest, version, readErr := a.readRetryProvider()
	if readErr != nil {
		return provider.FailureUnknown, a.persistenceError(readErr)
	}
	kind := provider.ClassifyFailure(err)
	if latest.Quota == "exhausted" {
		kind = provider.FailureQuota
	}
	if kind == provider.FailureAuth || kind == provider.FailureQuota {
		return kind, a.blockProvider(latest, version, kind)
	}
	return kind, nil
}

func (a *agentAttempt) blockProvider(latest model.Provider, version int, kind provider.FailureKind) error {
	if kind == provider.FailureAuth {
		latest.Available = false
		latest.Limitation = "Authentication required; use provider login and recheck setup"
	} else {
		latest.Quota = "exhausted"
	}
	if err := a.Store.Put(a.ctx, "providers", latest.ID, latest, version); err != nil {
		return a.persistenceError(err)
	}
	for n := range a.providers {
		if a.providers[n].ID == latest.ID {
			a.providers[n] = latest
		}
	}
	return nil
}

func (a *agentAttempt) waitRetry(attempt int, kind provider.FailureKind, failure error) error {
	delays := []time.Duration{2 * time.Second, 10 * time.Second}
	if kind == provider.FailureRateLimit {
		delays = []time.Duration{10 * time.Second, 30 * time.Second}
	}
	delay := delays[attempt-1]
	a.event("whirlwind/retry", map[string]any{"next_attempt": attempt + 1, "delay_ms": delay.Milliseconds(), "reason": failure.Error(), "session_id": a.state.SessionID})
	if a.eventErr != nil {
		return a.eventErr
	}
	if a.retryWait != nil {
		return a.retryWait(a.ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-a.ctx.Done():
		return a.ctx.Err()
	case <-timer.C:
		return a.ctx.Err()
	}
}

func (a *agentAttempt) readRetryProvider() (model.Provider, int, error) {
	record, err := a.Store.Get(a.ctx, "providers", a.chosen.ID)
	if err != nil {
		return model.Provider{}, 0, err
	}
	latest, err := store.Decode[model.Provider](record)
	return latest, record.Version, err
}

func terminalExecutionError(ctx context.Context, err error) error {
	if errors.Is(err, store.ErrPersistence) {
		return err
	}
	if cancelled := ctx.Err(); cancelled != nil {
		return cancelled
	}
	if provider.ClassifyFailure(err) == provider.FailurePolicy {
		return err
	}
	var transport *provider.TransportError
	if !errors.As(err, &transport) && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return err
	}
	return nil
}
