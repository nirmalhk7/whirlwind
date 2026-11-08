package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

func TestDiscoveryComparesOnlyCurrentApprovedPlansForItsProject(t *testing.T) {
	ctx := context.Background()
	engine, task, _ := testEngine(t)
	project, err := engine.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	approved := model.Plan{Objective: "Add bounded webhook retries", What: "Retry transient webhook delivery failures", Why: "Transient failures currently lose events", Benefit: "Events survive temporary outages", Where: []string{"internal/webhook"}, AcceptanceCriteria: []string{"Retry transient failures"}, Units: []model.Unit{{ID: "main", Objective: "Add retry policy"}}}
	approvedTask := model.Task{ID: "approved", ProjectID: project.ID, PlanVersion: 2, Plan: &approved, PlanDocument: &model.PlanDocument{Hash: "approved-hash", ApprovedHash: "approved-hash", PlanVersion: 2}}
	if err := engine.Store.Put(ctx, "tasks", approvedTask.ID, approvedTask, 0); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Put(ctx, "plans", "approved:2", approved, 0); err != nil {
		t.Fatal(err)
	}
	unapproved := model.Task{ID: "draft", ProjectID: project.ID, PlanVersion: 1, Plan: &model.Plan{Objective: "Draft secret webhook credential cleanup"}, PlanDocument: &model.PlanDocument{Hash: "draft-hash"}}
	if err := engine.Store.Put(ctx, "tasks", unapproved.ID, unapproved, 0); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Put(ctx, "plans", "draft:1", model.Plan{Objective: "Draft secret webhook credential cleanup"}, 0); err != nil {
		t.Fatal(err)
	}
	otherProject := model.Task{ID: "other", ProjectID: "different-project", PlanVersion: 1, Plan: &model.Plan{Objective: "Other project private deployment details"}, PlanDocument: &model.PlanDocument{Hash: "other-hash", ApprovedHash: "other-hash", PlanVersion: 1}}
	if err := engine.Store.Put(ctx, "tasks", otherProject.ID, otherProject, 0); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Put(ctx, "plans", "other:1", *otherProject.Plan, 0); err != nil {
		t.Fatal(err)
	}

	prompt, _, _, _, err := engine.prepareDiscovery(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Add bounded webhook retries") {
		t.Fatalf("ideation prompt omitted this project's approved plan context: %s", prompt)
	}
	if strings.Contains(prompt, "Draft secret webhook credential cleanup") || strings.Contains(prompt, "Other project private deployment details") {
		t.Fatalf("ideation prompt exposed an unapproved or other-project plan: %s", prompt)
	}
}

func TestDiscoveryRotatesTowardLessExploredRepositoryAreas(t *testing.T) {
	ctx := context.Background()
	engine, task, _ := testEngine(t)
	project, err := engine.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"src/a.go", "src/b.go"} {
		fullPath := filepath.Join(project.Path, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte("package src\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := exec.Command("git", "-C", project.Path, "add", ".")
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("stage repository fixture: %s: %v", output, err)
	}
	run = exec.Command("git", "-C", project.Path, "commit", "-m", "add discovery areas")
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("commit repository fixture: %s: %v", output, err)
	}
	prior := []model.Direction{
		{ID: "old-a", ProjectID: project.ID, Day: "2026-11-01", Category: "expand", Title: "Prior A", Problem: "Existing A issue", Mechanism: "Change A behavior", Components: []string{"src/a.go"}},
		{ID: "old-a-2", ProjectID: project.ID, Day: "2026-11-02", Category: "optimize", Title: "Prior A 2", Problem: "Another A issue", Mechanism: "Tune A behavior", Components: []string{"src/a.go"}},
	}
	if err := engine.Store.SaveDirections(ctx, project.ID, "2026-11-02", prior); err != nil {
		t.Fatal(err)
	}
	prompt, _, _, _, err := engine.prepareDiscovery(WithFreshDiscovery(ctx, true), project)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, `"src/b.go"`) || !strings.Contains(prompt, `"src/a.go"`) {
		t.Fatalf("ideation prompt omitted the tracked repository areas: %s", prompt)
	}
	if strings.Index(prompt, `"src/b.go"`) > strings.Index(prompt, `"src/a.go"`) {
		t.Fatalf("ideation did not rotate toward the less explored area: %s", prompt)
	}
}

func TestDiscoveryProvidesCurrentFileContentForNoveltyComparison(t *testing.T) {
	engine, task, _ := testEngine(t)
	project, err := engine.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	prompt, _, _, _, err := engine.prepareDiscovery(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "allowed_paths:") {
		t.Fatalf("current repository content was absent from ideation evidence: %s", prompt)
	}
}
