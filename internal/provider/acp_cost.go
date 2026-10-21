package provider

import "whirlwind/internal/model"

// ACP usage_update reports cumulative session cost, independently of context occupancy.
func (s *providerSession) acpCost(update map[string]any) {
	cost, ok := update["cost"].(map[string]any)
	if !ok {
		return
	}
	amount, valid := runtimeNumber(cost["amount"])
	currency, _ := cost["currency"].(string)
	if !valid || currency == "" {
		return
	}
	s.runtimeEvent("whirlwind/cost", map[string]any{"amount": amount, "currency": currency, "scope": "session"})
	// Usage.Cost and its consumers express USD; other currencies remain explicit evidence.
	if currency != "USD" {
		return
	}
	s.mu.Lock()
	if s.output.Usage == nil {
		s.output.Usage = &model.Usage{}
	}
	s.output.Usage.Scope = "session"
	s.output.Usage.Cost = &amount
	s.mu.Unlock()
}
