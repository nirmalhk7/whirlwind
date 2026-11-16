package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/model"
)

const confirmationAlreadyCurrent = "already-current"
const contractDefaultModel = "default-model"

// Removing effective-model validation would allow these rejected sessions to prompt.
func TestACPModelConfirmationBeforePrompt(t *testing.T) {
	for _, selector := range []string{"config", "legacy"} {
		for _, resume := range []bool{false, true} {
			for _, selection := range []string{"confirmed", "ignored", "wrong", "missing", "missing-current", "missing-option", confirmationAlreadyCurrent} {
				t.Run(fmt.Sprintf("%s/resume=%t/%s", selector, resume, selection), func(t *testing.T) {
					runModelConfirmation(t, selector, resume, selection)
				})
			}
		}
	}
}

func runModelConfirmation(t *testing.T, selector string, resume bool, selection string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	promptFile := filepath.Join(t.TempDir(), "prompted")
	fixture := protocolFixture{Kind: "acp", Scenario: "confirmation-" + selector, Model: contractSelectedModel, Resume: resume, Selection: selection, PromptFile: promptFile}
	input := Input{CWD: t.TempDir(), Prompt: contractPrompt, Model: contractSelectedModel}
	if resume {
		input.SessionID = testSessionID
	}
	out, err := contractAdapter(t, fixture).Run(ctx, input, Hooks{})
	_, promptErr := os.Stat(promptFile)
	if selection == "confirmed" || selection == confirmationAlreadyCurrent {
		if err != nil || out.Text != contractReply || out.ResolvedModel != contractSelectedModel || promptErr != nil {
			t.Fatalf("confirmed selection failed: %+v %v, prompt: %v", out, err, promptErr)
		}
	} else {
		if err == nil || !strings.Contains(err.Error(), "did not confirm selected model") || !strings.Contains(err.Error(), "adapter") {
			t.Fatalf("unconfirmed model accepted or no compatibility guidance: %+v %v", out, err)
		}
		if !os.IsNotExist(promptErr) || out.Text != "" || out.ResolvedModel == contractSelectedModel {
			t.Fatalf("rejected selection prompted or manufactured metadata: %+v, prompt: %v", out, promptErr)
		}
	}
}

func (p *protocolPeer) confirmationMetadata(f protocolFixture, current string) map[string]any {
	if strings.HasSuffix(f.Scenario, "config") {
		option := map[string]any{"id": contractModelOptionID, "category": "model", "type": "select", "options": []any{map[string]any{"value": contractSelectedModel, "name": contractSelectedModelName}}}
		if current != "" {
			option["currentValue"] = current
		}
		return map[string]any{"configOptions": []any{option}}
	}
	return map[string]any{"models": map[string]any{"currentModelId": current, "availableModels": []any{map[string]any{"modelId": contractSelectedModel, "name": contractSelectedModelName}}}}
}

func (p *protocolPeer) configureConfirmationModel(f protocolFixture) {
	if f.Selection == confirmationAlreadyCurrent {
		return
	}
	method := "session/set_model"
	if strings.HasSuffix(f.Scenario, "config") {
		method = "session/set_config_option"
	}
	params := p.read(method)
	if method == "session/set_model" {
		p.require(params["modelId"] == contractSelectedModel, "requested model lost")
	} else {
		p.require(params["configId"] == contractModelOptionID && params["value"] == contractSelectedModel, "requested model lost")
	}
	p.require(params["sessionId"] == testSessionID, "selection session lost")
	current := contractSelectedModel
	switch f.Selection {
	case "ignored":
		current = contractDefaultModel
	case "wrong":
		current = "wrong-model"
	case "missing-current":
		current = ""
	case "missing-option":
		p.reply(map[string]any{"configOptions": []any{}})
		return
	case "missing":
		p.reply(map[string]any{})
		return
	}
	p.reply(p.confirmationMetadata(f, current))
}

// A thinking response must confirm thinking without silently changing the model.
func TestACPThinkingRetainsConfirmedModel(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, tc := range []struct{ selection, wantError string }{
			{"confirmed", ""},
			{"thinking-ignored", "did not confirm selected thinking level"},
			{"thinking-missing", "did not confirm selected thinking level"},
			{"model-drift", "did not confirm selected model"},
		} {
			t.Run(fmt.Sprintf("resume=%t/%s", resume, tc.selection), func(t *testing.T) {
				runThinkingConfirmation(t, resume, tc.selection, tc.wantError)
			})
		}
	}
}

func runThinkingConfirmation(t *testing.T, resume bool, selection, wantError string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	promptFile := filepath.Join(t.TempDir(), "prompted")
	fixture := protocolFixture{Kind: "acp", Scenario: "thinking", Model: contractSelectedModel, Resume: resume, Selection: selection, PromptFile: promptFile}
	input := Input{CWD: t.TempDir(), Prompt: contractPrompt, Model: contractSelectedModel, Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "high"}}
	if resume {
		input.SessionID = testSessionID
	}
	out, err := contractAdapter(t, fixture).Run(ctx, input, Hooks{})
	_, promptErr := os.Stat(promptFile)
	if wantError == "" {
		if err != nil || out.Text != contractReply || out.ResolvedModel != contractSelectedModel || out.EffectiveThinking == nil || *out.EffectiveThinking != *input.Thinking || promptErr != nil {
			t.Fatalf("confirmed runtime settings lost: %+v %v, prompt: %v", out, err, promptErr)
		}
	} else if err == nil || !strings.Contains(err.Error(), wantError) || !os.IsNotExist(promptErr) || out.Text != "" {
		t.Fatalf("invalid runtime settings reached prompt: %+v %v, prompt: %v", out, err, promptErr)
	}
}
