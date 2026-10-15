package provider

import (
	"encoding/json"
	"fmt"

	"whirlwind/internal/model"
)

func optionValues(values []acpOptionValue) []ModelInfo {
	result := []ModelInfo{}
	for _, value := range values {
		if value.Value != "" {
			result = append(result, ModelInfo{value.Value, value.Name})
		}
		result = append(result, optionValues(value.Options)...)
	}
	return result
}

func (s acpSession) thinkingOptions() []ThinkingOption {
	result := []ThinkingOption{}
	for _, option := range s.ConfigOptions {
		if option.Category != "thought_level" || option.Type != "select" {
			continue
		}
		var current string
		_ = json.Unmarshal(option.Current, &current)
		result = append(result, ThinkingOption{option.ID, option.Name, current, optionValues(option.Options)})
	}
	return result
}

// setACPConfig consumes the agent's full response, including model-dependent options.
func (s *providerSession) setACPConfig(session *acpSession, id, value string) error {
	var response struct {
		ConfigOptions []acpConfigOption `json:"configOptions"`
	}
	if err := s.rpc.Call(s.ctx, "session/set_config_option", map[string]any{"sessionId": session.ID, "configId": id, "value": value}, &response); err != nil {
		return err
	}
	if response.ConfigOptions != nil {
		session.ConfigOptions = response.ConfigOptions
	} else {
		if s.input.Thinking != nil && id == s.input.Thinking.ConfigID {
			return fmt.Errorf("ACP did not confirm selected thinking level %q", value)
		}
		// Older model selectors returned an empty response. Retain advertised options.
		for n := range session.ConfigOptions {
			if session.ConfigOptions[n].ID == id {
				session.ConfigOptions[n].Current, _ = json.Marshal(value)
			}
		}
	}
	return nil
}

func (s *providerSession) setACPModel(session *acpSession) error {
	if s.input.Model == "" {
		return nil
	}
	catalog, configID := session.modelCatalog()
	found := false
	for _, item := range catalog.Models {
		found = found || item.ID == s.input.Model
	}
	if !found {
		return fmt.Errorf("ACP model selection unavailable for %q; load supported models and choose an advertised model", s.input.Model)
	}
	if configID != "" {
		return s.setACPConfig(session, configID, s.input.Model)
	}
	if err := s.rpc.Call(s.ctx, "session/set_model", map[string]any{"sessionId": session.ID, "modelId": s.input.Model}, nil); err != nil {
		return err
	}
	session.Models.Current = s.input.Model
	return nil
}

func (s *providerSession) setACPThinking(session *acpSession) error {
	thinking := s.input.Thinking
	if thinking == nil {
		return nil
	}
	if !session.advertisesThinking(*thinking) {
		return fmt.Errorf("ACP thinking level %q is not advertised for selected model", thinking.Value)
	}
	if err := s.setACPConfig(session, thinking.ConfigID, thinking.Value); err != nil {
		return err
	}
	if !session.confirmsThinking(*thinking) {
		return fmt.Errorf("ACP did not confirm selected thinking level %q", thinking.Value)
	}
	return nil
}

func (session acpSession) advertisesThinking(thinking model.ThinkingChoice) bool {
	for _, option := range session.thinkingOptions() {
		if option.ConfigID != thinking.ConfigID {
			continue
		}
		for _, value := range option.Options {
			if value.ID == thinking.Value {
				return true
			}
		}
	}
	return false
}

func (session acpSession) confirmsThinking(thinking model.ThinkingChoice) bool {
	for _, current := range session.thinkingOptions() {
		if current.ConfigID == thinking.ConfigID && current.Current == thinking.Value {
			return true
		}
	}
	return false
}
