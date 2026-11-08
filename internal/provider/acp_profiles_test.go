package provider

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

func TestBundledACPProfilesDefineLaunchAuthAndDynamicSelection(t *testing.T) {
	want := map[string][]string{
		"claude": {"claude-agent-acp"},
		"codex":  {"codex-acp"},
		"cursor": {"agent", "acp"},
	}
	defaults := Defaults("")
	for _, profile := range defaults[:3] {
		expected, ok := want[profile.ID]
		if !ok || !reflect.DeepEqual(profile.Command, expected) || profile.Kind != "acp" {
			t.Errorf("default %s profile = kind %q command %v, want ACP %v", profile.ID, profile.Kind, profile.Command, expected)
		}
		configured, ok := ACPProfileByID(profile.ID)
		if !ok || len(configured.AuthReferences) == 0 || configured.InstallHint == "" || configured.ModelConfigCategory != "model" || configured.ThinkingConfigCategory != "thought_level" {
			t.Errorf("default %s profile lacks bundled auth or dynamic model/thinking mapping: %+v", profile.ID, configured)
		}
	}
}

func TestRunRejectsLegacyNativeTransportBeforeStartingProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	legacy := model.Provider{ID: "legacy-codex", Kind: "codex", Command: []string{"codex", "app-server", "--listen", "stdio://"}}
	_, err := (ProcessAdapter{Provider: legacy}).Run(ctx, Input{CWD: t.TempDir()}, Hooks{})
	if err == nil || !strings.Contains(err.Error(), "configure an ACP agent command and set kind: acp") {
		t.Fatalf("legacy native provider should pause with an actionable ACP migration error, got %v", err)
	}
}

func TestACPFailuresTellHumanHowToInstallAndAuthenticate(t *testing.T) {
	missing := actionableACPError(model.Provider{ID: "codex", ACPProfile: "codex"}, errors.New("executable not found"))
	if !strings.Contains(missing.Error(), "npm install --global @agentclientprotocol/codex-acp@2.5.0") {
		t.Fatalf("missing runtime error lacks install guidance: %v", missing)
	}
	unauthorized := authenticationACPError(model.Provider{ID: "codex", ACPProfile: "codex"}, errors.New("unauthorized"))
	if !strings.Contains(unauthorized.Error(), "CODEX_API_KEY") || !strings.Contains(unauthorized.Error(), "provider login") {
		t.Fatalf("missing auth error lacks scoped auth guidance: %v", unauthorized)
	}
}
