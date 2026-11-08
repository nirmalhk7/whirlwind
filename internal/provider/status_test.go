package provider

import (
	"context"
	"testing"
	"time"

	"whirlwind/internal/model"
)

func TestProviderConnectivityNeverSendsInference(t *testing.T) {
	for _, profile := range []string{"", "codex", "claude"} {
		name := profile
		if name == "" {
			name = "acp"
		}
		for _, fail := range []bool{false, true} {
			mode := "ok"
			scenario := "metadata-only"
			if fail {
				mode = "fail"
				scenario = contractInitError
			}
			t.Run(name+"/"+mode, func(t *testing.T) {
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
			})
		}
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
