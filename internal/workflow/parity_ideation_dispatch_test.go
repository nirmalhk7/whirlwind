package workflow

import (
	"context"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestParityInitialIdeationDispatchIgnoresPeriodicDisable(t *testing.T) {
	e, _, _ := testEngine(t)
	ctx := context.Background()
	settings := DefaultSettings()
	settings.Ideation.Cron = ""
	record, _ := e.Store.Get(ctx, "settings", "global")
	if err := e.Store.Put(ctx, "settings", "global", settings, record.Version); err != nil {
		t.Fatal(err)
	}
	// No usable adapter: intake must still consume the durable queue entry and
	// record an actionable outcome instead of silently ignoring onboarding.
	if err := e.Store.Put(ctx, "operations", "initial", model.Operation{Kind: "ideation", Subject: "p", Stage: "queued", StartedAt: "2026-11-10T00:00:00Z", Input: map[string]string{"day": "2026-11-10"}}, 0); err != nil {
		t.Fatal(err)
	}
	e.Daily(ctx)
	e.background.Wait()
	run, err := e.Store.Get(ctx, "operations", "initial")
	if err != nil {
		t.Fatal(err)
	}
	op, err := store.Decode[model.Operation](run)
	if err != nil || op.Stage == "queued" {
		t.Fatalf("initial ideation ignored while periodic schedule disabled: %+v %v", op, err)
	}
}
