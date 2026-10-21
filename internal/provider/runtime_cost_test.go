package provider

import (
	"math"
	"testing"
)

func TestACPReportedCostRemainsSessionScoped(t *testing.T) {
	s := &providerSession{}
	s.sessionUpdate(acpCostUpdate(.25, "USD"))
	assertACPSessionCost(t, s, .25)
	s.sessionUpdate(acpCostUpdate(.5, "USD"))
	assertACPSessionCost(t, s, .5)
	s.sessionUpdate(map[string]any{"update": map[string]any{"sessionUpdate": "usage_update", "used": float64(10), "size": float64(100)}})
	assertACPSessionCost(t, s, .5)
}

func acpCostUpdate(amount float64, currency string) map[string]any {
	return map[string]any{"update": map[string]any{"sessionUpdate": "usage_update", "used": float64(80), "size": float64(100), "cost": map[string]any{"amount": amount, "currency": currency}}}
}

func assertACPSessionCost(t *testing.T, s *providerSession, amount float64) {
	t.Helper()
	usage := s.output.Usage
	if usage == nil {
		t.Fatal("reported cumulative cost lost")
	}
	if usage.Scope != "session" || usage.Cost == nil || *usage.Cost != amount {
		t.Fatalf("cost scope/value: %+v", usage)
	}
	if usage.TotalTokens != nil || s.output.Cost != nil {
		t.Fatal("session cost/occupancy represented as attempt consumption")
	}
}

func TestACPNonUSDCostRetainsCurrencyInEvidence(t *testing.T) {
	var cost map[string]any
	s := &providerSession{hooks: Hooks{Event: func(kind string, value any) {
		if kind == "whirlwind/cost" {
			cost, _ = value.(map[string]any)
		}
	}}}
	s.sessionUpdate(acpCostUpdate(.4, "EUR"))
	if s.output.Usage != nil && s.output.Usage.Cost != nil {
		t.Fatal("non-USD cost presented as dollars")
	}
	if cost["currency"] != "EUR" || cost["amount"] != .4 || cost["scope"] != "session" {
		t.Fatalf("currency evidence lost: %+v", cost)
	}
}

func TestRuntimeRejectsInvalidContextNumbers(t *testing.T) {
	for _, used := range []any{math.NaN(), math.Inf(1), 3.5, -1.0, 1e100, "85"} {
		s := &providerSession{}
		s.contextUsage(map[string]any{"used": used, "size": 1e100}, "acp")
		if s.output.Context != nil {
			t.Fatalf("invalid context accepted: %v -> %+v", used, s.output.Context)
		}
	}
}

func TestACPRejectsNonfiniteAndInvalidReportedCosts(t *testing.T) {
	for _, amount := range []any{math.NaN(), math.Inf(1), -1.0, "0.25", nil} {
		costEvents := 0
		s := &providerSession{hooks: Hooks{Event: func(kind string, _ any) {
			if kind == "whirlwind/cost" {
				costEvents++
			}
		}}}
		update := acpCostUpdate(0, "USD")
		value := update["update"].(map[string]any)
		value["cost"].(map[string]any)["amount"] = amount
		s.sessionUpdate(update)
		if costEvents != 0 {
			t.Fatalf("invalid cost retained as runtime evidence: %v", amount)
		}
		if s.output.Usage != nil && s.output.Usage.Cost != nil {
			t.Fatalf("invalid cost retained as usage: %v", amount)
		}
	}
}
