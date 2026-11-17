package provider

import (
	"context"
	"os"
	"testing"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

// Opt-in metadata discovery against installed, authenticated ACP adapters; no prompt.
func TestACPModelCatalogLive(t *testing.T) {
	if os.Getenv("WHIRLWIND_TEST_ACP_MODELS_LIVE") != "1" {
		t.Skip("set WHIRLWIND_TEST_ACP_MODELS_LIVE=1 to verify ACP model discovery")
	}
	for _, p := range Defaults() {
		if p.Kind != "acp" {
			continue
		}
		t.Run(p.ID, func(t *testing.T) {
			checkLiveACPModelCatalog(t, p)
		})
	}
}

// Initialization is independently useful when a runtime exits before catalog
// discovery. It uses no credentials or integration grants. Keep diagnostics
// redacted through the production RPC reader; authenticated discovery is separate.
func TestACPInitializationLive(t *testing.T) {
	if os.Getenv("WHIRLWIND_TEST_ACP_INITIALIZATION_LIVE") != "1" {
		t.Skip("set WHIRLWIND_TEST_ACP_INITIALIZATION_LIVE=1 to verify sandboxed ACP initialization")
	}
	for _, p := range Defaults() {
		if p.Kind == "acp" {
			t.Run(p.ID, func(t *testing.T) { checkLiveACPInitialization(t, p) })
		}
	}
}

func checkLiveACPInitialization(t *testing.T, p model.Provider) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	profile, err := execution.ForProvider(t.TempDir(), true, "", p.Sandbox, p.Command)
	if err != nil {
		t.Fatal(err)
	}
	profile, err = pinStandaloneProfile(ctx, profile, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	transferred := false
	defer func() {
		if !transferred {
			if err := completeStandaloneProfile(profile); err != nil {
				t.Error(err)
			}
		}
	}()
	input := Input{ProviderID: p.ID, ACPProfile: p.ACPProfile, CWD: profile.CWD, ReadOnly: true, Execution: &profile}
	session := &providerSession{ctx: ctx, input: input, client: NewClient(input, Hooks{})}
	defer func() { _ = session.client.Close() }()
	rpc, err := StartWithProfile(ctx, p.Command, profile, session.handle)
	if err != nil {
		t.Fatal(actionableACPError(p, err))
	}
	transferred = true
	session.rpc = rpc
	defer func() {
		if err := rpc.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := session.initializeACP(); err != nil {
		rpc.mu.Lock()
		diagnostic := rpc.stderrTail
		rpc.mu.Unlock()
		t.Logf("redacted initialization diagnostics: %s", diagnostic)
		t.Fatal(metadataACPError(p, err))
	}
}

func checkLiveACPModelCatalog(t *testing.T, p model.Provider) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	catalog, err := Models(ctx, p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) == 0 {
		t.Fatal("ACP returned no models")
	}
	for _, model := range catalog.Models {
		if model.ID == "" {
			t.Fatal("ACP returned empty model ID")
		}
	}
	t.Logf("ACP advertised %d models; current=%s", len(catalog.Models), catalog.Current)
}
