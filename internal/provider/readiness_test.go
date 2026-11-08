package provider

import (
	"context"
	"testing"
	"time"
	"whirlwind/internal/model"
)

func TestReadinessAppliesExactACPConfigurationWithoutInference(t *testing.T) {
	adapter := contractAdapter(t, protocolFixture{Kind: "acp", Scenario: "thinking-readiness", Model: contractSelectedModel, ReadOnly: true})
	probe, ok := any(adapter).(interface {
		CheckReadiness(context.Context, Input) error
	})
	if !ok {
		t.Fatal("adapter has no exact-configuration readiness check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	input := Input{CWD: t.TempDir(), ReadOnly: true, Model: contractSelectedModel, Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "high"}}
	if err := probe.CheckReadiness(ctx, input); err != nil {
		t.Fatal(err)
	}
	input.Thinking.Value = "unsupported"
	if err := probe.CheckReadiness(ctx, input); err == nil {
		t.Fatal("unsupported thinking marked ready")
	}
}
