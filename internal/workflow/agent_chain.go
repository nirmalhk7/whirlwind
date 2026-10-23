package workflow

import (
	"errors"
	"fmt"
	"slices"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func (a *agentRun) routeConfigured(config model.AgentConfig) (provider.Output, error) {
	return a.routeChoices(config.Chain)
}

func (a *agentRun) routeChoices(choices []model.AgentChoice) (provider.Output, error) {
	var failures []error
	var waiting *AdmissionWait
	for _, choice := range choices {
		if err := a.ctx.Err(); err != nil {
			return provider.Output{}, err
		}
		source := a.configuredProvider(choice.Provider)
		if _, err := a.quotaAdmission(source); err != nil {
			var wait *AdmissionWait
			if errors.As(err, &wait) {
				copyWait := *wait
				copyWait.Reason = fmt.Sprintf("%s/%s: %s", choice.Provider, choice.Model, wait.Reason)
				wait = &copyWait
				if waiting == nil || wait.NextCheck.Before(waiting.NextCheck) {
					waiting = wait
				}
				failures = append(failures, err)
				continue
			}
			return provider.Output{}, err
		}
		if err := a.configuredProviderError(source); err != nil {
			failures = append(failures, fmt.Errorf("%s/%s: skipped (0 attempts): %w", choice.Provider, choice.Model, err))
			a.fallbackReason = err.Error()
			continue
		}
		output, advance, err := a.executeChoice(source, choice)
		if !advance {
			if provider.ClassifyFailure(err) == provider.FailurePolicy {
				return output, err
			}
			var wait *AdmissionWait
			if errors.As(err, &wait) {
				if waiting == nil || wait.NextCheck.Before(waiting.NextCheck) {
					waiting = wait
				}
				failures = append(failures, err)
				continue
			}
			return output, err
		}
		if provider.ClassifyFailure(err) == provider.FailureQuota {
			_, admissionErr := a.quotaAdmission(a.configuredProvider(choice.Provider))
			var wait *AdmissionWait
			if errors.As(admissionErr, &wait) {
				copyWait := *wait
				copyWait.Cause = err
				copyWait.Reason = err.Error() + "; " + wait.Reason
				waiting = &copyWait
			}
		}
		failures = append(failures, err)
		a.fallbackReason = err.Error()
		a.input.Prompt += "\nPrevious provider unavailable. Continue existing worktree from handoff; preserve completed work.\n" + jsonText(a.task.Handoff)
	}
	if waiting != nil {
		return provider.Output{}, waiting
	}
	return provider.Output{}, errors.Join(append([]error{fmt.Errorf("agent %s exhausted its provider/model fallback list; inspect attempt evidence, correct provider setup if needed, then resume the task", a.input.Role)}, failures...)...)
}

func (a *agentRun) configuredProviderError(source model.Provider) error {
	if a.input.Role == "submit" && slices.Contains(source.Capabilities, "code") && !slices.Contains(source.Capabilities, "submit") {
		source.Capabilities = append(slices.Clone(source.Capabilities), "submit")
	}
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
