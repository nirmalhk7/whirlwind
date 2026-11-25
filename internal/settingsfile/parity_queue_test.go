package settingsfile

import (
	"testing"

	"whirlwind/internal/model"
)

func TestParityQueueAllowsThinkingOnlyFallback(t *testing.T) {
	p := map[string]Provider{"p": {ID: "p", Kind: "acp", Capabilities: []string{"code"}}}
	chain := []model.AgentChoice{
		{Provider: "p", Model: "model", Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "high"}},
		{Provider: "p", Model: "model", Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "low"}},
	}
	if err := validateChain("code", chain, p); err != nil {
		t.Fatalf("thinking-only fallback rejected: %v", err)
	}
	if err := validateChain("code", append(chain, chain[0]), p); err == nil {
		t.Fatal("identical queue entry accepted twice")
	}
}
