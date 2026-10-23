package workflow

import (
	"context"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

// A persisted wait must wake once after refreshed metadata, but a manual pause must stay paused.
func TestQuotaWaitSurvivesRestartAndHonorsManualPause(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	r, _ := e.Store.Get(ctx, "tasks", task.ID)
	task.Stage = "waiting_quota"
	task.Wait = &model.TaskWait{Kind: "quota", Provider: "codex", Pool: "codex", Role: "code", ResumeStage: "coding", NextCheckAt: time.Now().Add(-time.Minute).Format(time.RFC3339Nano), PlanVersion: task.PlanVersion}
	if err := e.Store.Put(ctx, "tasks", task.ID, task, r.Version); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(ctx, "providers", "codex", model.Provider{ID: "codex", Kind: "codex", Available: true, Quota: "exhausted", Capabilities: []string{"code"}}, 0); err != nil {
		t.Fatal(err)
	}
	restarted := New(e.Store, e.GitHub, e.Root)
	restarted.Adapter = func(model.Provider) provider.Adapter {
		return recoveredQuotaAdapter{quotaReaderAdapter{state: "available"}}
	}
	restarted.refreshQuotaWaits(ctx)
	r, _ = e.Store.Get(ctx, "tasks", task.ID)
	saved, _ := store.Decode[model.Task](r)
	if saved.Stage != "coding" || saved.Wait != nil {
		t.Fatalf("wait failed to wake: %+v (%s)", saved, cwd)
	}
	saved.Stage = "paused"
	saved.Wait = task.Wait
	if err := e.Store.Put(ctx, "tasks", task.ID, saved, r.Version); err != nil {
		t.Fatal(err)
	}
	restarted.refreshQuotaWaits(ctx)
	r, _ = e.Store.Get(ctx, "tasks", task.ID)
	saved, _ = store.Decode[model.Task](r)
	if saved.Stage != "paused" {
		t.Fatal("manual pause auto-resumed")
	}
}
