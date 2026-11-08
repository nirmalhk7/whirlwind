package workflow

import (
	"context"
	"errors"
	"slices"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func completeAcceptedLocalDeliveryFixture(t *testing.T, engine *Engine, task model.Task, project model.Project, record model.Record) model.Task {
	t.Helper()
	ctx := context.Background()
	step := &taskStep{Engine: engine, ctx: ctx, project: project, task: task, record: record}
	for range 3 {
		finished, err := step.submitting()
		if err != nil {
			t.Fatalf("finish exact local delivery: %v", err)
		}
		if finished {
			break
		}
		if err := engine.Store.Put(ctx, "tasks", step.task.ID, step.task, step.record.Version); err != nil {
			t.Fatal(err)
		}
		step.record.Version++
	}
	if step.task.Stage != "complete" || step.task.Delivery == nil || step.task.Delivery.Stage != "complete" {
		t.Fatalf("local delivery fixture did not complete: stage=%q delivery=%+v", step.task.Stage, step.task.Delivery)
	}
	return step.task
}

func approvedUnitCheckpointFixture(t *testing.T) (*Engine, model.Task, model.Project, []int) {
	t.Helper()
	engine, task, _ := testEngine(t)
	project, err := engine.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	units := []model.Unit{
		{ID: "a", Objective: "Create both files owned by unit a.", AllowedPaths: []string{"a-one.txt", "a-two.txt", resultFile}},
		{ID: "b", Objective: "Create both files owned by unit b.", AllowedPaths: []string{"b-one.txt", "b-two.txt", resultFile}},
	}
	task = approveCheckpointPlan(t, engine, project, task, units)
	return engine, task, project, []int{0, 1}
}

func approveCheckpointPlan(t *testing.T, engine *Engine, project model.Project, task model.Task, units []model.Unit) model.Task {
	t.Helper()
	ctx := context.Background()
	where := []string{resultFile}
	for _, unit := range units {
		for _, path := range unit.AllowedPaths {
			if !slices.Contains(where, path) {
				where = append(where, path)
			}
		}
	}
	command := []string{"sh", "-c", "test -f result.txt"}
	task.Mode = model.AgenticMode
	task.Stage = "planning"
	task.PlanVersion = 1
	task.Worktree, task.Branch, task.Base = "", "", ""
	task.Plan = &model.Plan{
		Objective:                "Integrate independent implementation units",
		What:                     "Create each unit's files and combine the committed results.",
		Why:                      "The repository needs all scoped changes in one candidate.",
		Benefit:                  "Each approved unit checkpoint is retained in the candidate.",
		Where:                    where,
		Scope:                    []string{"Write only the paths listed by each implementation unit."},
		Exclusions:               []string{"Do not change the source checkout or another unit's files."},
		Steps:                    []string{"Create and commit each unit's scoped files.", "Integrate every durable unit commit into the task candidate."},
		RepositoryTestCommands:   [][]string{command},
		RepositoryTestInventory:  "Configured: .whirlwind.yaml checks.test runs sh -c 'test -f result.txt'.",
		RepositoryBuildInventory: "None configured: inspected the temporary repository's committed policy and files.",
		Checks:                   []model.Check{{Name: "approved-result-check", Command: command}},
		AcceptanceCriteria:       []string{"Every unit's committed files are present in the candidate."},
		Requirements:             []model.Requirement{{ID: "REQ-UNITS", Outcome: "Every approved unit checkpoint is integrated once into this candidate.", EvidenceMethod: "Inspect the integrated commit tree and each persisted unit checkpoint."}},
		Units:                    units,
	}
	record, err := engine.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Put(ctx, "tasks", task.ID, task, record.Version); err != nil {
		t.Fatal(err)
	}
	if err := engine.WriteLocalPlan(ctx, project, &task); err != nil {
		t.Fatalf("publish local plan for human approval: %v", err)
	}
	record, err = engine.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Put(ctx, "tasks", task.ID, task, record.Version); err != nil {
		t.Fatal(err)
	}
	view, err := engine.ReadLocalPlan(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	record, err = engine.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.ReviewLocalPlan(ctx, task.ID, record.Version, "approve", view.Hash, ""); err != nil {
		t.Fatalf("approve detailed plan: %v", err)
	}
	approved, _, err := localStoredTaskRecord(ctx, engine.Store, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Stage != "coding" || approved.PlanDocument == nil || approved.PlanDocument.ApprovedHash != view.Hash {
		t.Fatalf("human approval did not freeze the plan: %+v", approved)
	}
	return approved
}

func localStoredTaskRecord(ctx context.Context, database *store.Store, id string) (model.Task, int, error) {
	record, err := database.Get(ctx, "tasks", id)
	if err != nil {
		return model.Task{}, 0, err
	}
	task, err := store.Decode[model.Task](record)
	return task, record.Version, err
}

type unexpectedUnitAdapter struct{}

func (unexpectedUnitAdapter) Run(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
	return provider.Output{}, errors.New("completed durable unit was executed again")
}

func validateIntegratedUnitCommits(t *testing.T, task model.Task) {
	t.Helper()
	if task.Plan == nil {
		t.Fatal("approved unit plan missing")
	}
	for _, unit := range task.Plan.Units {
		if unit.Commit == "" || unit.Branch == "" || unit.Stage != "integrated" {
			t.Fatalf("unit checkpoint not integrated: %+v", unit)
		}
	}
}
