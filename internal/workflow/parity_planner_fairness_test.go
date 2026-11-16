package workflow

import (
	"context"
	"path/filepath"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func TestParityPlannerBacklogBoundsLaunchesAndRotatesProjects(t *testing.T) {
	e, existing, _ := testEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); e.background.Wait() })
	current, _ := e.Store.Get(ctx, "execution_plans", existing.ID)
	existing.Stage = "complete"
	if err := e.Store.Put(ctx, "execution_plans", existing.ID, existing, current.Version); err != nil {
		t.Fatal(err)
	}
	settingsRecord, _ := e.Store.Get(ctx, "settings", "global")
	settings := e.settings(ctx)
	settings.GlobalSlots, settings.ProjectSlots = 1, 2
	if err := e.Store.Put(ctx, "settings", "global", settings, settingsRecord.Version); err != nil {
		t.Fatal(err)
	}
	project, err := e.Project(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	project.ID, project.Name = "q", "Other project"
	if err := e.Store.Put(ctx, "projects", "q", project, 0); err != nil {
		t.Fatal(err)
	}
	e.PlansRoot = t.TempDir()
	for _, entry := range []struct{ id, project, run string }{{"older", "p", "historical"}, {"newer", "p", "recent"}, {"other", "q", "other-run"}} {
		if err := e.Store.Put(ctx, "operations", entry.run, model.Operation{Kind: "ideation", Subject: entry.project, Stage: "complete"}, 0); err != nil {
			t.Fatal(err)
		}
		idea := model.Idea{Title: "Invoice export", ID: entry.id, ProjectID: entry.project, RunID: entry.run, Direction: "expand", What: entry.id, Why: "Improve result", Where: "result.txt", HowItHelps: "Keep useful output", DecisionStatus: "draft", NoveltyMethod: "legacy_llm"}
		if err := e.Store.Put(ctx, "ideas", idea.ID, idea, 0); err != nil {
			t.Fatal(err)
		}
		idea.DecisionStatus = "approved"
		if err := e.Store.Put(ctx, "ideas", idea.ID, idea, 1); err != nil {
			t.Fatal(err)
		}
	}
	started := make(chan string, 4)
	release := make(chan struct{}, 4)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
			started <- filepath.Base(filepath.Dir(input.CWD))
			select {
			case <-release:
				return provider.Output{Text: "No output"}, nil
			case <-ctx.Done():
				return provider.Output{}, ctx.Err()
			}
		})
	}
	next := func() string {
		t.Helper()
		select {
		case id := <-started:
			return id
		case <-time.After(5 * time.Second):
			t.Fatal("backlog not dispatched")
			return ""
		}
	}
	e.dispatch(ctx)
	if first := next(); first != model.PlanID("idea:older") {
		t.Fatalf("historical backlog skipped: %s", first)
	}
	e.mu.Lock()
	running := len(e.running)
	e.mu.Unlock()
	if running != 1 {
		t.Fatalf("global capacity queued %d active plan workers, want one", running)
	}
	release <- struct{}{}
	e.background.Wait()
	// Reconstruct the supervisor to verify that project fairness is durable.
	replacement := New(e.Store, e.GitHub, e.Root)
	replacement.PlansRoot = e.PlansRoot
	replacement.Adapter = e.Adapter
	replacement.dispatch(ctx)
	select {
	case id := <-started:
		if id != model.PlanID("idea:other") {
			cancel()
			replacement.background.Wait()
			t.Fatalf("one project's newer run starved another project's approved backlog: %s", id)
		}
	case <-time.After(5 * time.Second):
		cancel()
		replacement.background.Wait()
		t.Fatal("second project not dispatched")
	}
	release <- struct{}{}
	replacement.background.Wait()
}
