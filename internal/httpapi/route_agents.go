package httpapi

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (q apiRequest) agents() error {
	if err := q.validateAgentRole(); err != nil {
		return err
	}
	if q.r.Method == "PATCH" {
		return q.saveAgent()
	}
	records, err := q.agentResponse()
	if err != nil {
		return err
	}
	reply(q.w, 200, records)
	return nil
}

func (q apiRequest) validateAgentRole() error {
	if q.r.Method == "PATCH" && q.id == "" {
		return &apiError{404, "not_found", "Choose an agent role"}
	}
	if q.id != "" && !slices.Contains(model.AgentRoles(), q.id) {
		return &apiError{404, "not_found", "Unknown agent role"}
	}
	return nil
}

func (q apiRequest) agentResponse() (any, error) {
	if q.id != "" {
		return q.agentRecord(q.id)
	}
	records := []model.Record{}
	for _, role := range model.AgentRoles() {
		record, err := q.agentRecord(role)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func (q apiRequest) agentRecord(role string) (model.Record, error) {
	record, err := q.Store.Get(q.r.Context(), "agents", role)
	if errors.Is(err, store.ErrNotFound) {
		data, _ := json.Marshal(model.AgentConfig{Role: role, Chain: []model.AgentChoice{}})
		return model.Record{ID: role, Data: data}, nil
	}
	return record, err
}

func (q apiRequest) saveAgent() error {
	n, err := expected(q.r)
	if q.r.Header.Get("If-Match") == "0" {
		n, err = 0, nil
	}
	if err != nil {
		return &apiError{428, "precondition_required", err.Error()}
	}
	var config model.AgentConfig
	if err := json.Unmarshal(q.body, &config); err != nil {
		return err
	}
	config.Role = q.id
	if err := q.validateAgentChain(&config); err != nil {
		return err
	}
	q.save(q.w, q.r, "agents", q.id, config, n, 200)
	return nil
}

func (q apiRequest) validateAgentChain(config *model.AgentConfig) error {
	if len(config.Chain) == 0 {
		return &apiError{422, "invalid_input", "Choose a primary provider and model"}
	}
	seen := map[[2]string]bool{}
	for i, choice := range config.Chain {
		choice.Model = strings.TrimSpace(choice.Model)
		config.Chain[i] = choice
		key := [2]string{choice.Provider, choice.Model}
		if err := q.validateAgentChoice(choice, seen[key]); err != nil {
			return err
		}
		seen[key] = true
	}
	return nil
}

func (q apiRequest) validateAgentChoice(choice model.AgentChoice, duplicate bool) error {
	if choice.Thinking != nil && (strings.TrimSpace(choice.Thinking.ConfigID) == "" || strings.TrimSpace(choice.Thinking.Value) == "") {
		return &apiError{422, "invalid_input", "Thinking needs an advertised config ID and value"}
	}
	if choice.Provider == "" || choice.Model == "" || duplicate {
		return &apiError{422, "invalid_input", "Each choice needs a provider and model; duplicate choices are not allowed"}
	}
	return q.validateAgentProvider(choice.Provider)
}

func (q apiRequest) validateAgentProvider(id string) error {
	record, err := q.Store.Get(q.r.Context(), "providers", id)
	if err != nil {
		return &apiError{422, "invalid_input", "Selected provider does not exist"}
	}
	p, err := store.Decode[model.Provider](record)
	if err != nil {
		return err
	}
	if p.Kind == "unavailable" || !slices.Contains(p.Capabilities, q.id) {
		return &apiError{422, "invalid_input", "Provider does not support this agent role"}
	}
	return nil
}
