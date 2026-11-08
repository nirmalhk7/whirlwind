package settingsfile

import (
	"encoding/json"
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"
	"whirlwind/internal/model"
	"whirlwind/internal/workflow"
)

func TestFinalizerAndLegacySubmitUseIndependentQueues(t *testing.T) {
	providers := map[string]Provider{
		"codex": {ID: "codex", Name: "Codex", Kind: "acp", Command: []string{"npx", "--yes", "@agentclientprotocol/codex-acp"}, Capabilities: []string{"finalizer", "submit"}},
	}
	finalizer := []model.AgentChoice{{Provider: "codex", Model: "model-a"}}
	submit := []model.AgentChoice{{Provider: "codex", Model: "model-b"}}
	if err := validateChain("finalizer", finalizer, providers); err != nil {
		t.Fatalf("finalizer chain rejected: %v", err)
	}
	if err := validateChain("submit", submit, providers); err != nil {
		t.Fatalf("legacy submit chain should remain valid independently: %v", err)
	}
	if got := model.AgentRoles(); len(got) != 5 || got[4] != "finalizer" {
		t.Fatalf("canonical agent roles = %v, want ideate, plan, code, review, finalizer", got)
	}
}

func TestBundledACPProfileCanOmitCommandButLegacyNativeCommandRemainsReadable(t *testing.T) {
	legacy := Provider{ID: "old-codex", Kind: "codex", Command: []string{"codex", "app-server", "--listen", "stdio://"}}
	if err := validateProviderCommand(legacy); err != nil {
		t.Fatalf("legacy records must remain loadable so they can be migrated without data loss: %v", err)
	}
}

func TestSettingsYAMLRetainsBundledACPProfileReference(t *testing.T) {
	doc := Document{Version: 1, Workers: workflow.DefaultSettings(), Providers: []Provider{{ID: "cursor", Name: "Cursor", Kind: "acp", Capabilities: []string{"ideate"}}}, Agents: map[string][]model.AgentChoice{}}
	raw, err := encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = yaml.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	providers := fields["providers"].([]any)
	providers[0].(map[string]any)["acp_profile"] = "cursor"
	raw, err = yaml.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decode(raw)
	if err != nil {
		t.Fatalf("decode bundled profile settings: %v", err)
	}
	if err = validateProviderCommand(decoded.Providers[0]); err != nil {
		t.Fatalf("bundled ACP profile should supply a command: %v", err)
	}
	resolved := managed(decoded.Providers[0], model.Provider{})
	if got, want := resolved.Command, []string{"agent", "acp"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("profile-resolved command = %v, want %v", got, want)
	}
	encoded, err := json.Marshal(decoded.Providers[0])
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got["acp_profile"] != "cursor" {
		t.Fatalf("saved provider profile = %v, want cursor", got["acp_profile"])
	}
}
