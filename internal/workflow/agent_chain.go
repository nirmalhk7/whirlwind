package workflow

import (
	"errors"
	"fmt"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func (a *agentRun) routeConfigured(config model.AgentConfig) (provider.Output, error) {
	if len(config.Chain) == 0 {
		return provider.Output{}, fmt.Errorf("no configured agent queue for role %q; configure its ordered provider/model/thinking entries", a.input.Role)
	}
	return a.routeChoices(config.Chain)
}

func (a *agentRun) routeChoices(choices []model.AgentChoice) (provider.Output, error) {
	var failures []error
	var waiting *AdmissionWait
	for index, choice := range choices {
		a.queuePosition = index + 1
		a.queueLength = len(choices)
		if err := a.ctx.Err(); err != nil {
			return provider.Output{}, err
		}
		source, wait, failure, fatal, skipped := a.preflightRouteChoice(choice)
		if fatal != nil {
			return provider.Output{}, fatal
		}
		if wait != nil {
			waiting = earlierQuotaWait(waiting, wait)
			failures = append(failures, failure)
			continue
		}
		if skipped {
			failures = append(failures, failure)
			continue
		}
		outcome := a.executeRouteChoice(source, choice)
		if outcome.wait != nil {
			waiting = earlierQuotaWait(waiting, outcome.wait)
		}
		if outcome.failure != nil {
			failures = append(failures, outcome.failure)
		}
		if !outcome.continueRouting {
			return outcome.output, outcome.failure
		}
	}
	return a.exhaustedRouteResult(waiting, failures)
}

func (a *agentRun) exhaustedRouteResult(waiting *AdmissionWait, failures []error) (provider.Output, error) {
	if waiting != nil {
		return provider.Output{}, waiting
	}
	failures = append([]error{fmt.Errorf("agent %s exhausted its provider/model fallback list; inspect attempt evidence, correct provider setup if needed, then resume the task", a.input.Role)}, failures...)
	return provider.Output{}, errors.Join(failures...)
}

type routeChoiceOutcome struct {
	output          provider.Output
	failure         error
	wait            *AdmissionWait
	continueRouting bool
}

func (a *agentRun) preflightRouteChoice(choice model.AgentChoice) (model.Provider, *AdmissionWait, error, error, bool) {
	source := a.configuredProvider(choice.Provider)
	if _, err := a.quotaAdmissionForChoice(source, choice); err != nil {
		var wait *AdmissionWait
		if !errors.As(err, &wait) {
			return source, nil, nil, err, false
		}
		copyWait := *wait
		copyWait.Reason = fmt.Sprintf("%s/%s: %s", choice.Provider, choice.Model, wait.Reason)
		return source, &copyWait, err, nil, false
	}
	if err := a.configuredProviderError(source); err != nil {
		a.fallbackReason = err.Error()
		failure := fmt.Errorf("%s/%s: skipped (0 attempts): %w", choice.Provider, choice.Model, err)
		return source, nil, failure, nil, true
	}
	return source, nil, nil, nil, false
}

func (a *agentRun) executeRouteChoice(source model.Provider, choice model.AgentChoice) routeChoiceOutcome {
	output, advance, err := a.executeChoice(source, choice)
	if errors.Is(err, ErrActionOutcomeUncertain) {
		return routeChoiceOutcome{output: output, failure: err}
	}
	if !advance {
		if provider.ClassifyFailure(err) == provider.FailurePolicy {
			return routeChoiceOutcome{output: output, failure: err}
		}
		var wait *AdmissionWait
		if errors.As(err, &wait) {
			return routeChoiceOutcome{failure: err, wait: wait, continueRouting: true}
		}
		return routeChoiceOutcome{output: output, failure: err}
	}
	if provider.ClassifyFailure(err) == provider.FailureQuota {
		_, admissionErr := a.quotaAdmission(a.configuredProvider(choice.Provider))
		var wait *AdmissionWait
		if errors.As(admissionErr, &wait) {
			copyWait := *wait
			copyWait.Cause = err
			copyWait.Reason = err.Error() + "; " + wait.Reason
			return a.fallbackChoice(err, &copyWait)
		}
	}
	return a.fallbackChoice(err, nil)
}

func (a *agentRun) fallbackChoice(err error, wait *AdmissionWait) routeChoiceOutcome {
	a.fallbackReason = err.Error()
	a.input.Prompt += "\nPrevious provider unavailable. Continue existing worktree from handoff; preserve completed work.\n" + jsonText(a.task.Handoff)
	return routeChoiceOutcome{failure: err, wait: wait, continueRouting: true}
}

func earlierQuotaWait(current, next *AdmissionWait) *AdmissionWait {
	if current == nil || next.NextCheck.Before(current.NextCheck) {
		return next
	}
	return current
}

func (a *agentRun) configuredProviderError(source model.Provider) error {
	if _, err := provider.Route([]model.Provider{source}, a.input.Role, nil, nil); err != nil {
		reason := err.Error()
		if source.Quota == "exhausted" {
			reason = "quota exhausted"
			if source.ResetAt != "" {
				reason += "; reset at " + source.ResetAt
			}
		}
		if source.Limitation != "" {
			reason += "; " + source.Limitation
		}
		return errors.New(reason)
	}
	return nil
}

func (a *agentRun) configuredProvider(id string) model.Provider {
	for _, candidate := range a.providers {
		if candidate.ID == id {
			return candidate
		}
	}
	return model.Provider{}
}
