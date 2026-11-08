package workflow

import (
	"testing"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func TestSetupIdentityIgnoresOperationAndRuntimeState(t *testing.T) {
	source := model.Provider{ID: "codex", Kind: "codex", Command: []string{"codex", "app-server"}}
	profile := execution.Profile{Version: "fixture", CWD: "/workspace", ReadOnly: true, RuntimeDir: "/runtime/one"}
	first := provider.Input{Model: "configured", Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "low"}, Execution: &profile, OperationID: "first"}
	identity := setupFingerprint(source, first)
	otherProfile := profile
	otherProfile.RuntimeDir = "/runtime/two"
	second := first
	second.Execution = &otherProfile
	second.OperationID = "second"
	second.Prompt = "different prompt"
	if setupFingerprint(source, second) != identity {
		t.Fatal("operation bookkeeping defeated configuration probe deduplication")
	}
	second.Thinking = &model.ThinkingChoice{ConfigID: "effort", Value: "high"}
	if setupFingerprint(source, second) == identity {
		t.Fatal("thinking configurations shared readiness")
	}
}
