package workflow

import (
	"errors"
	"fmt"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func (a *agentRun) routeConfigured(config model.AgentConfig) (provider.Output, error) {
	for _, choice := range config.Chain {
		if err := a.ctx.Err(); err != nil {
			return provider.Output{}, err
		}
		source := a.configuredProvider(choice.Provider)
		if _, err := provider.Route([]model.Provider{source}, a.input.Role, nil, nil); err != nil {
			continue
		}
		a.input.Model = choice.Model
		a.input.Thinking = choice.Thinking
		a.input.SessionID = ""
		execution, err := a.newAttempt(source)
		if err != nil {
			return provider.Output{}, err
		}
		output, err := execution.execute()
		if execution.retry(err) {
			continue
		}
		if execution.eventErr != nil {
			return output, errors.Join(err, execution.eventErr)
		}
		return output, err
	}
	return provider.Output{}, fmt.Errorf("agent %s exhausted its configured provider/model fallback list", a.input.Role)
}

func (a *agentRun) configuredProvider(id string) model.Provider {
	for _, candidate := range a.providers {
		if candidate.ID == id {
			return candidate
		}
	}
	return model.Provider{}
}
