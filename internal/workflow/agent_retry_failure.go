package workflow

import (
	"context"
	"errors"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func (a *agentAttempt) failureKind(err error) (provider.FailureKind, error) {
	if errors.Is(err, ErrMalformedRoleOutput) {
		return provider.FailureTransient, nil
	}
	if errors.Is(err, ErrRuntimeHealthLost) {
		return provider.FailureTransient, nil
	}
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

func (a *agentAttempt) readRetryProvider() (model.Provider, int, error) {
	record, err := a.Store.Get(a.ctx, "providers", a.chosen.ID)
	if err != nil {
		return model.Provider{}, 0, err
	}
	latest, err := store.Decode[model.Provider](record)
	return latest, record.Version, err
}

func terminalExecutionError(ctx context.Context, err error) error {
	if terminal := terminalFailure(err); terminal != nil {
		return terminal
	}
	return ctx.Err()
}

func terminalFailure(err error) error {
	if terminalSetupFailure(err) || terminalPersistenceFailure(err) {
		return err
	}
	if isAdmissionWait(err) {
		return err
	}
	var transport *provider.TransportError
	if !errors.As(err, &transport) && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return err
	}
	return nil
}

func terminalSetupFailure(err error) bool {
	return errors.Is(err, execution.ErrSandbox) || provider.ClassifyFailure(err) == provider.FailurePolicy
}

func terminalPersistenceFailure(err error) bool {
	return errors.Is(err, store.ErrBudgetExceeded) || errors.Is(err, store.ErrPersistence)
}

func isAdmissionWait(err error) bool {
	var wait *AdmissionWait
	return errors.As(err, &wait)
}
