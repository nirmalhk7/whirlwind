package provider

import (
	"testing"
)

func TestACPContextOccupancyIsNotCumulativeConsumption(t *testing.T) {
	s := &providerSession{}
	s.sessionUpdate(map[string]any{"update": map[string]any{"sessionUpdate": "usage_update", "used": float64(85), "size": float64(100), "cost": map[string]any{"amount": .25, "currency": "USD"}}})
	if s.output.Context == nil || s.output.Context.Used != 85 || s.output.Context.Size != 100 {
		t.Fatalf("missing occupancy: %+v", s.output)
	}
	if s.output.Usage != nil && s.output.Usage.TotalTokens != nil {
		t.Fatal("occupancy counted as token consumption")
	}
	s.sessionUpdate(map[string]any{"update": map[string]any{"sessionUpdate": "usage_update", "used": float64(15), "size": float64(100)}})
	if s.output.Context.Used != 15 {
		t.Fatal("compaction reduction ignored")
	}
}

func TestACPPartialToolUpdatePreservesIdentity(t *testing.T) {
	s := &providerSession{}
	s.sessionUpdate(map[string]any{"update": map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t", "kind": "execute", "rawInput": map[string]any{"command": "test"}, "status": "in_progress"}})
	s.sessionUpdate(map[string]any{"update": map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t", "status": "failed", "rawOutput": "test failed"}})
	if len(s.tools) != 1 || s.tools["t"].Kind != "execute" || s.tools["t"].InputHash == "" || s.tools["t"].OutputHash == "" {
		t.Fatalf("partial update lost identity: %+v", s.tools)
	}
}
