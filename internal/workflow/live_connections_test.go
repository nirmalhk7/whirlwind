package workflow_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/settingsfile"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

// Live tests are deliberately opt-in. They read native connection metadata and
// destination capabilities; they never create or update a pull request.
func TestLiveNativeConnections(t *testing.T) {
	if os.Getenv("WHIRLWIND_LIVE_INTEGRATIONS") != "1" {
		t.Skip("opt in with WHIRLWIND_LIVE_INTEGRATIONS=1 and WHIRLWIND_LIVE_SETTINGS")
	}
	state, engine := liveConnectionEngine(t)
	checkLiveProviders(t, state, engine)
	checkLiveDestinations(t, engine)
}

func liveConnectionEngine(t *testing.T) (*store.Store, *workflow.Engine) {
	t.Helper()
	settings := os.Getenv("WHIRLWIND_LIVE_SETTINGS")
	if settings == "" {
		t.Fatal("set WHIRLWIND_LIVE_SETTINGS to the configured settings.yaml")
	}
	raw, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	copyPath := filepath.Join(root, "settings.yaml")
	if err = os.WriteFile(copyPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx := context.Background()
	if err = settingsfile.Attach(ctx, state, copyPath); err != nil {
		t.Fatal(err)
	}
	engine := workflow.New(state, nil, root)
	return state, engine
}

func checkLiveProviders(t *testing.T, state *store.Store, engine *workflow.Engine) {
	t.Helper()
	ctx := context.Background()
	providers, err := state.List(ctx, "providers")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range providers {
		p, err := store.Decode[model.Provider](record)
		if err != nil {
			t.Fatal(err)
		}
		t.Run("provider-"+p.ID, func(t *testing.T) {
			checkLiveProvider(t, engine, p)
		})
	}
}

func checkLiveProvider(t *testing.T, engine *workflow.Engine, p model.Provider) {
	t.Helper()
	if p.Kind == "unavailable" {
		t.Skip("provider explicitly unavailable")
	}
	probe, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	inventory, err := engine.IntegrationInventory(probe, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, connection := range inventory {
		if connection.Enabled && !connection.Callable {
			t.Errorf("%s unavailable: %s", connection.Ref, connection.Error)
		}
	}
}

func checkLiveDestinations(t *testing.T, engine *workflow.Engine) {
	t.Helper()
	ctx := context.Background()
	var repositories map[string]string
	if configured := os.Getenv("WHIRLWIND_LIVE_REPOSITORIES"); configured != "" {
		if err := json.Unmarshal([]byte(configured), &repositories); err != nil {
			t.Fatal(err)
		}
	}
	config := engine.WorkflowConfiguration(ctx)
	for remote := range config.Integrations.Publication {
		t.Run("destination-"+remote, func(t *testing.T) {
			path := repositories[remote]
			if path == "" {
				t.Fatal("add canonical repository URL and local checkout path to WHIRLWIND_LIVE_REPOSITORIES JSON")
			}
			configured, hash, err := policy.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			project := model.Project{ID: "live-" + model.Fingerprint([]byte(remote))[:12], Repo: remote, RemoteURL: remote, Path: path, Policy: configured, PolicyHash: hash}
			probe, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			if _, err := engine.InspectIntegrationRepository(probe, project); err != nil {
				t.Fatal(err)
			}
		})
	}
}
