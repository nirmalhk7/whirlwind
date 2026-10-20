package provider

import (
	"encoding/json"
	"whirlwind/internal/model"
)

func (s *providerSession) sessionUpdate(p map[string]any) {
	u, _ := p["update"].(map[string]any)
	if u["sessionUpdate"] == "config_option_update" {
		raw, _ := json.Marshal(u["configOptions"])
		var options []acpConfigOption
		if json.Unmarshal(raw, &options) == nil {
			s.recordACPConfig(options, "")
		}
	}
	if u["sessionUpdate"] == "agent_message_chunk" {
		content, _ := u["content"].(map[string]any)
		txt, _ := content["text"].(string)
		s.mu.Lock()
		s.output.Text += txt
		s.mu.Unlock()
	}
}

func (s *providerSession) messageDelta(p map[string]any) {
	txt, _ := p["delta"].(string)
	s.mu.Lock()
	s.output.Text += txt
	s.mu.Unlock()
}

func (s *providerSession) quotaUpdate(m Message) {
	if s.hooks.Event != nil {
		s.hooks.Event("whirlwind/quota", json.RawMessage(m.Params))
	}
}

func (s *providerSession) tokenUsage(p map[string]any) {
	usage, ok := p["tokenUsage"].(map[string]any)
	if !ok {
		return
	}
	total, ok := usage["total"].(map[string]any)
	if !ok {
		return
	}
	value := &model.Usage{Scope: "session"}
	number := func(key string) *int64 {
		if n, ok := total[key].(float64); ok {
			v := int64(n)
			return &v
		}
		return nil
	}
	value.TotalTokens = number("totalTokens")
	value.InputTokens = number("inputTokens")
	value.OutputTokens = number("outputTokens")
	value.CachedTokens = number("cachedInputTokens")
	s.mu.Lock()
	s.output.Usage = value
	s.output.Tokens = nil
	s.mu.Unlock()
}

func (s *providerSession) turnCompleted(p map[string]any) {
	if s.hooks.Event != nil {
		s.hooks.Event("whirlwind/stop", p)
	}
	turn, _ := p["turn"].(map[string]any)
	status, _ := turn["status"].(string)
	if status == "failed" && turn["error"] != nil {
		b, _ := json.Marshal(turn["error"])
		s.mu.Lock()
		s.turnFailure = codexTurnFailure(b)
		s.mu.Unlock()
	}
	select {
	case s.completed <- status:
	default:
	}
}

func (s *providerSession) claudeEvent(p map[string]any) {
	sid, _ := p["session_id"].(string)
	resolved, _ := p["resolved_model"].(string)
	s.mu.Lock()
	changed := sid != "" && sid != s.output.SessionID
	if sid != "" {
		s.output.SessionID = sid
	}
	if resolved != "" {
		s.output.ResolvedModel = resolved
	}
	s.output.Text += claudeText(p)
	if raw, ok := p["usage"].(map[string]any); ok {
		b, _ := json.Marshal(raw)
		var usage model.Usage
		if json.Unmarshal(b, &usage) == nil {
			s.output.Usage = &usage
		}
	}
	s.mu.Unlock()
	if changed && s.hooks.Event != nil {
		s.hooks.Event("whirlwind/session", map[string]any{"session_id": sid, "resumed": s.input.SessionID != "" && sid == s.input.SessionID})
	}
	if resolved != "" && s.hooks.Event != nil {
		s.hooks.Event("whirlwind/config", map[string]string{"resolved_model": resolved})
	}
	if p["type"] == "result" && s.hooks.Event != nil {
		s.hooks.Event("whirlwind/stop", map[string]any{"stop_reason": p["stop_reason"], "status": p["subtype"]})
	}
}

func claudeText(event map[string]any) string {
	if event["type"] != "assistant" {
		return ""
	}
	message, _ := event["message"].(map[string]any)
	content, _ := message["content"].([]any)
	text := ""
	for _, part := range content {
		if block, ok := part.(map[string]any); ok && block["type"] == "text" {
			value, _ := block["text"].(string)
			text += value
		}
	}
	return text
}
