package provider

import (
	"context"
	"testing"
	"time"

	"whirlwind/internal/model"
)

func TestProviderConnectivityNeverSendsInference(t *testing.T) {
	for _, profile := range []struct{ id, label string }{{"", "acp"}, {"codex", "codex"}, {"claude", "claude"}} {
		t.Run(profile.label+"/ok", func(t *testing.T) {
			assertConnectivityMetadataOnly(t, profile.id, "metadata-only", false)
		})
		t.Run(profile.label+"/fail", func(t *testing.T) {
			assertConnectivityMetadataOnly(t, profile.id, contractInitError, true)
		})
	}
}

func assertConnectivityMetadataOnly(t *testing.T, profile, scenario string, fail bool) {
	t.Helper()
	t.Setenv("WHIRLWIND_DATA_DIR", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	adapter := contractAdapter(t, protocolFixture{Kind: "acp", ACPProfile: profile, Scenario: scenario, ReadOnly: true})
	detail, err := CheckConnection(ctx, adapter.Provider, t.TempDir())
	if (err != nil) != fail {
		t.Fatalf("failure=%v error=%v", fail, err)
	}
	if !fail && detail != "Protocol session responding; inference not tested." {
		t.Fatalf("unexpected metadata-only status %q", detail)
	}
}

func TestProviderConnectivityCancellationBoundsHungCommands(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := CheckConnection(ctx, model.Provider{Kind: "claude", Command: []string{"sh", "-c", "sleep 60"}}, t.TempDir())
	if err == nil || time.Since(started) > 2*time.Second {
		t.Fatalf("hung check not bounded: %v (%v)", err, time.Since(started))
	}
}
