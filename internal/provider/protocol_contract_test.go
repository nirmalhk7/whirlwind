package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/testutil/openshell"
)

const contractPrompt = "deliver café 👋\nsecond line"

const contractReply = "received café 👋\nsecond line"

const (
	contractModelConfig       = "model-config"
	contractModelLegacy       = "model-legacy"
	contractSelectedModel     = "selected-model"
	contractThinkingCatalog   = "thinking-catalog"
	contractCatalogConfig     = "catalog-config"
	contractCatalogLegacy     = "catalog-legacy"
	contractSelectedModelName = "Selected model"
	contractModelOptionID     = "provider-model"

	contractNoLoad       = "no-load"
	contractEmptyLoad    = "load-empty-result"
	contractInitError    = "initialize-error"
	contractAuthError    = "authentication required"
	contractEmptySession = "empty-session"
	contractTurnID       = "turn-one"
)

type protocolFixture struct {
	Kind       string
	ACPProfile string
	Scenario   string
	Resume     bool
	ReadOnly   bool
	Model      string
	Chunks     []string
	Auth       *protocolAuthExpectation
}

func TestExplicitModelSelection(t *testing.T) {
	for _, tc := range []struct{ kind, scenario string }{{"acp", contractModelConfig}, {"acp", contractModelLegacy}, {"acp", "model-unsupported"}} {
		t.Run(tc.kind+"/"+tc.scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := contractAdapter(t, protocolFixture{Kind: tc.kind, Scenario: tc.scenario, Model: contractSelectedModel}).Run(ctx, Input{CWD: t.TempDir(), Prompt: contractPrompt, Model: contractSelectedModel}, Hooks{})
			if tc.scenario == "model-unsupported" {
				if err == nil || !strings.Contains(err.Error(), "model selection") {
					t.Fatalf("model silently ignored: %v", err)
				}
				return
			}
			if err != nil || out.Text != contractReply {
				t.Fatalf("selected model round trip: %+v %v", out, err)
			}
		})
	}
}

func TestACPThinkingAppliesAfterModelForNewAndResumedSessions(t *testing.T) {
	for _, resume := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		input := Input{CWD: t.TempDir(), Prompt: contractPrompt, Model: contractSelectedModel}
		_ = json.Unmarshal([]byte(`{"Thinking":{"config_id":"effort","value":"high"}}`), &input)
		if resume {
			input.SessionID = testSessionID
		}
		out, err := contractAdapter(t, protocolFixture{Kind: "acp", Scenario: "thinking", Model: contractSelectedModel, Resume: resume}).Run(ctx, input, Hooks{})
		cancel()
		if err != nil || out.Text != contractReply {
			t.Fatalf("resume=%v: %+v %v", resume, out, err)
		}
	}
}

func TestThinkingCatalogUsesModelResponseWithoutInference(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	adapter := contractAdapter(t, protocolFixture{Kind: "acp", Scenario: contractThinkingCatalog, Model: contractSelectedModel, ReadOnly: true})
	catalog, err := Models(ctx, adapter.Provider, t.TempDir(), contractSelectedModel)
	if err != nil || catalog.Current != contractSelectedModel {
		t.Fatalf("model metadata: %+v %v", catalog, err)
	}
	if len(catalog.ThinkingOptions) != 1 {
		t.Fatalf("thinking options: %+v", catalog)
	}
	option := catalog.ThinkingOptions[0]
	if option.ConfigID != "effort" || len(option.Options) != 1 || option.Options[0].ID != "high" {
		t.Fatalf("model-dependent thinking lost: %+v %v", catalog, err)
	}
}

func TestUnsupportedACPThinkingFailsBeforePrompt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := contractAdapter(t, protocolFixture{Kind: "acp", Scenario: contractModelConfig, Model: contractSelectedModel}).Run(ctx, Input{CWD: t.TempDir(), Prompt: contractPrompt, Model: contractSelectedModel, Thinking: &model.ThinkingChoice{ConfigID: "effort", Value: "high"}}, Hooks{})
	if err == nil || !strings.Contains(err.Error(), "not advertised") || out.Text != "" {
		t.Fatalf("thinking silently ignored: %+v %v", out, err)
	}
}

func TestModelCatalogUsesProviderMetadataWithoutPrompt(t *testing.T) {
	for _, tc := range []struct{ kind, scenario string }{{"codex", contractCatalogConfig}, {"claude", contractCatalogLegacy}, {"acp", contractCatalogConfig}, {"acp", contractCatalogLegacy}} {
		t.Run(tc.kind+"/"+tc.scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			fixture := protocolFixture{Kind: "acp", Scenario: tc.scenario, ReadOnly: true}
			if tc.kind != "acp" {
				fixture.Auth = catalogAuthExpectation(tc.kind)
			}
			adapter := contractAdapter(t, fixture)
			if tc.kind != "acp" {
				adapter.Provider.ModelCommand = adapter.Provider.Command
				adapter.Provider.Command = []string{"must-not-use-native-model-discovery"}
				adapter.Provider.Kind = tc.kind
			}
			catalog, err := Models(ctx, adapter.Provider, t.TempDir())
			if err != nil || len(catalog.Models) != 1 || catalog.Models[0].ID != contractSelectedModel || catalog.Models[0].Name != contractSelectedModelName {
				t.Fatalf("catalog lost: %+v %v", catalog, err)
			}
		})
	}
}

func contractAdapter(t *testing.T, fixture protocolFixture) ProcessAdapter {
	t.Helper()
	installProtocolOpenShell(t)
	if fixture.ACPProfile != "" && fixture.Auth == nil {
		fixture.Auth = catalogAuthExpectation(fixture.ACPProfile)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return ProcessAdapter{Provider: model.Provider{Kind: "acp", ACPProfile: fixture.ACPProfile, Command: []string{exe, "-test.run=^TestProtocolContractHelper$", "--", string(encoded)}}}
}

func installProtocolOpenShell(t *testing.T) *openshell.Fixture {
	t.Helper()
	fixture := openshell.Install(t)
	fixture.EnableCommandExecution()
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	t.Setenv("WHIRLWIND_OPENSHELL_IMAGE", "provider-protocol-test-image")
	home := filepath.Join(fixture.Directory, "synthetic-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	for _, path := range []string{".codex/auth.json", ".claude/.credentials.json", protocolCursorCredentialPath} {
		file := filepath.Join(home, path)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("synthetic test credential"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fixture.ProjectSyntheticNativeFiles(t, home)
	return fixture
}

// Rejecting/reordering outgoing messages or losing streamed chunks breaks this test.
func TestProtocolMessageContracts(t *testing.T) {
	for _, kind := range []string{"acp"} {
		for _, resume := range []bool{false, true} {
			for _, readOnly := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/resume=%t/readOnly=%t", kind, resume, readOnly), func(t *testing.T) {
					verifyProtocolMessages(t, protocolFixture{Kind: kind, Resume: resume, ReadOnly: readOnly})
				})
			}
		}
	}
}
