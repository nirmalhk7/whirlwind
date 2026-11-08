package provider

import (
	"context"
	"strings"
	"testing"
	"whirlwind/internal/model"
)

func TestNativeInferenceTransportFailsWithACPRemediation(t *testing.T) {
	for _, kind := range []string{"codex", "claude"} {
		t.Run(kind, func(t *testing.T) {
			input := Input{CWD: t.TempDir(), SessionID: "legacy-session", Prompt: "must not launch", ReadOnly: true}
			out, err := (ProcessAdapter{Provider: model.Provider{ID: kind, Kind: kind, Command: []string{"native-command"}}}).Run(context.Background(), input, Hooks{})
			if err == nil || !strings.Contains(err.Error(), "unsupported native "+kind+" transport") || !strings.Contains(err.Error(), "set kind: acp") {
				t.Fatalf("legacy inference transport did not fail with actionable ACP guidance: %v", err)
			}
			if out.SessionID != input.SessionID {
				t.Fatalf("legacy session identity was lost: %+v", out)
			}
		})
	}
}
