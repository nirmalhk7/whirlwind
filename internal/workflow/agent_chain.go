package workflow

import (
	"errors"
	"fmt"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func (a *agentRun) routeConfigured(config model.AgentConfig) (provider.Output, error) {
	return a.routeChoices(config.Chain)
}

func (a *agentRun) routeChoices(choices []model.AgentChoice) (provider.Output, error) {
	var failures []error
	for _, choice := range choices {
		if err := a.ctx.Err(); err != nil {
			return provider.Output{}, err
		}
		source := a.configuredProvider(choice.Provider)
		if err := a.configuredProviderError(source); err != nil {
			failures = append(failures, fmt.Errorf("%s/%s: skipped (0 attempts): %w", choice.Provider, choice.Model, err))
			a.fallbackReason = err.Error()
			continue
		}
		output, advance, err := a.executeChoice(source, choice)
		if !advance {
			return output, err
		}
		failures = append(failures, err)
		a.fallbackReason = err.Error()
		a.input.Prompt += "\nPrevious provider unavailable. Continue existing worktree from handoff; preserve completed work.\n" + jsonText(a.task.Handoff)
	}
	return provider.Output{}, errors.Join(append([]error{fmt.Errorf("agent %s exhausted its provider/model fallback list; inspect attempt evidence, correct provider setup if needed, then resume the task", a.input.Role)}, failures...)...)
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
