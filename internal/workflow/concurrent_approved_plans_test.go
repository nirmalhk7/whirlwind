package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

const (
	concurrentPlanAFile = "plan-a.txt"
	concurrentPlanBFile = "plan-b.txt"
)

type concurrentPlanCodeStart struct {
	unit string
	cwd  string
}

type concurrentPlanCodeAdapter struct {
	started chan concurrentPlanCodeStart
	release chan struct{}
}

func (a *concurrentPlanCodeAdapter) Run(ctx context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
	if input.Role != "code" {
		return provider.Output{}, fmt.Errorf("unexpected role %q", input.Role)
	}
	a.started <- concurrentPlanCodeStart{unit: input.UnitID, cwd: input.CWD}
	select {
	case <-a.release:
	case <-ctx.Done():
		return provider.Output{}, ctx.Err()
	}
	if err := os.WriteFile(filepath.Join(input.CWD, resultFile), []byte("verified\n"), 0600); err != nil {
		return provider.Output{}, err
	}
	name, content := concurrentPlanFile(input.UnitID)
	if err := os.WriteFile(filepath.Join(input.CWD, name), []byte(content), 0600); err != nil {
		return provider.Output{}, err
	}
	return provider.Output{Text: "checkpoint written", SessionID: input.UnitID}, nil
}

func concurrentPlanFile(unit string) (string, string) {
	switch unit {
	case "plan-a-unit":
		return concurrentPlanAFile, "change owned by plan A\n"
	case "plan-b-unit":
		return concurrentPlanBFile, "change owned by plan B\n"
	default:
		return "unexpected.txt", "unexpected unit\n"
	}
}

func TestApprovedPlansForSameRepositoryExecuteConcurrentlyInIndependentWorktrees(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, first, _ := testEngine(t)
	project, err := e.Project(ctx, first.ProjectID)
	if err != nil {
		t.Fatal(err)
	}

	tasks := []model.Task{
		approveConcurrentPlan(t, e, project, first, "plan-a", "plan-a-unit", concurrentPlanAFile),
		approveConcurrentPlan(t, e, project, first, "plan-b", "plan-b-unit", concurrentPlanBFile),
	}
	for _, task := range tasks {
		if task.Stage != "coding" || task.PlanDocument == nil || task.PlanDocument.ApprovedHash == "" {
			t.Fatalf("plan %s did not reach coding after its separate approval: stage=%q document=%+v", task.ID, task.Stage, task.PlanDocument)
		}
	}

	adapter := &concurrentPlanCodeAdapter{started: make(chan concurrentPlanCodeStart, len(tasks)), release: make(chan struct{})}
	e.Adapter = func(model.Provider) provider.Adapter { return adapter }
	for index := range tasks {
		if err := e.prepareWorkspace(ctx, project, &tasks[index]); err != nil {
			t.Fatalf("prepare plan %s workspace: %v", tasks[index].ID, err)
		}
	}

	var workers sync.WaitGroup
	errorsFound := make(chan error, len(tasks))
	for index := range tasks {
		task := tasks[index]
		unit := &task.Plan.Units[0]
		workers.Add(1)
		go func() {
			defer workers.Done()
			errorsFound <- e.codeUnit(ctx, project, task, unit, jsonText(planForUnit(*task.Plan, 0)))
		}()
	}

	starts := make([]concurrentPlanCodeStart, 0, len(tasks))
	for range tasks {
		select {
		case start := <-adapter.started:
			starts = append(starts, start)
		case <-ctx.Done():
			cancel()
			workers.Wait()
			t.Fatalf("approved plans did not enter coding concurrently: starts=%+v: %v", starts, ctx.Err())
		}
	}
	if starts[0].cwd == starts[1].cwd || starts[0].unit == starts[1].unit {
		close(adapter.release)
		workers.Wait()
		t.Fatalf("approved plans shared a coder worktree or unit: %+v", starts)
	}
	close(adapter.release)
	workers.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}

	seenUnits := map[string]concurrentPlanCodeStart{}
	for _, start := range starts {
		seenUnits[start.unit] = start
	}
	for index := range tasks {
		task := tasks[index]
		unit := task.Plan.Units[0]
		start := seenUnits[unit.ID]
		if start.cwd == "" {
			t.Fatalf("plan %s coder did not reach its own unit", task.ID)
		}
		if task.Worktree == tasks[1-index].Worktree || task.Branch == tasks[1-index].Branch {
			t.Fatalf("plans do not have distinct integration worktrees and branches: A=%+v B=%+v", tasks[0], tasks[1])
		}
		if start.cwd == starts[0].cwd && start.cwd == starts[1].cwd {
			t.Fatalf("plan %s coder reused a worktree", task.ID)
		}
		if got := submissionGit(t, start.cwd, "branch", "--show-current"); got != unit.Branch {
			t.Fatalf("plan %s unit branch=%q, recorded=%q", task.ID, got, unit.Branch)
		}
		if got := submissionGit(t, start.cwd, "rev-parse", "HEAD"); got != unit.Commit {
			t.Fatalf("plan %s checkpoint commit=%q, worktree HEAD=%q", task.ID, unit.Commit, got)
		}
		file, content := concurrentPlanFile(unit.ID)
		actual, err := os.ReadFile(filepath.Join(start.cwd, file))
		if err != nil || string(actual) != content {
			t.Fatalf("plan %s lost its independent change: content=%q err=%v", task.ID, actual, err)
		}
		other, _ := concurrentPlanFile(tasks[1-index].Plan.Units[0].ID)
		if _, err := os.Stat(filepath.Join(start.cwd, other)); !os.IsNotExist(err) {
			t.Fatalf("plan %s worktree contains other plan's change %s: %v", task.ID, other, err)
		}
		changed := strings.Fields(submissionGit(t, start.cwd, "diff", "--name-only", unit.Base+".."+unit.Commit))
		sort.Strings(changed)
		want := []string{file, resultFile}
		sort.Strings(want)
		if !slices.Equal(changed, want) {
			t.Fatalf("plan %s commit contains unexpected or cross-plan files: got=%v want=%v", task.ID, changed, want)
		}
		persisted, err := e.Store.Get(ctx, "units", unitKey(task.ID, task.PlanVersion, unit.ID))
		if err != nil {
			t.Fatalf("plan %s completed unit checkpoint missing: %v", task.ID, err)
		}
		checkpoint, err := store.Decode[model.Unit](persisted)
		if err != nil || checkpoint.Stage != "complete" || checkpoint.Commit != unit.Commit || checkpoint.Branch != unit.Branch {
			t.Fatalf("plan %s checkpoint does not bind its branch and commit: %+v err=%v", task.ID, checkpoint, err)
		}
	}
	if tasks[0].ProjectID != tasks[1].ProjectID || tasks[0].ProjectID != project.ID {
		t.Fatalf("test did not exercise two plans for one repository: %+v", tasks)
	}
}

func approveConcurrentPlan(t *testing.T, e *Engine, project model.Project, template model.Task, id, unitID, file string) model.Task {
	t.Helper()
	ctx := context.Background()
	task := template
	task.ID = id
	task.Mode = model.AgenticMode
	task.PlanVersion = 1
	task.Stage = "planning"
	task.Error = ""
	task.Worktree, task.Branch, task.Base = "", "", ""
	task.Plan = &model.Plan{
		Objective:                "Implement independent change " + id,
		What:                     "Add " + file,
		Why:                      "This plan owns a separate proposed change.",
		Benefit:                  "Each approved plan retains its own candidate.",
		Where:                    []string{file, resultFile},
		Scope:                    []string{"Write only the approved plan files in its own worktree."},
		Exclusions:               []string{"Do not copy another plan's files or commits."},
		Steps:                    []string{"Create the approved change in the isolated unit worktree.", "Commit a task-scoped checkpoint."},
		RepositoryTestInventory:  "None configured: the concurrency fixture uses the repository policy's test check separately.",
		RepositoryBuildInventory: "None configured: inspected fixture repository and workflow configuration.",
		Checks:                   []model.Check{{Name: "plan-check", Command: []string{"sh", "-c", "test -f result.txt"}}},
		AcceptanceCriteria:       []string{"The approved file is present in this plan's candidate."},
		Requirements:             []model.Requirement{{ID: "REQ-" + strings.ToUpper(id), Outcome: "The plan's candidate contains " + file + " and no sibling plan file.", EvidenceMethod: "Inspect the isolated unit worktree and committed file list."}},
		Units:                    []model.Unit{{ID: unitID, Objective: "Write " + file + " and commit this plan's checkpoint.", AllowedPaths: []string{file, resultFile}}},
	}
	if id != template.ID {
		task.Issue = model.Issue{Title: task.Plan.Objective, Body: task.Plan.What}
		if template.Selection != nil {
			selection := *template.Selection
			selection.CandidateID = "selection-" + id
			task.Selection = &selection
		}
	}
	record, err := e.Store.Get(ctx, "tasks", task.ID)
	expected := 0
	if err == nil {
		expected = record.Version
	} else if err != store.ErrNotFound {
		t.Fatal(err)
	}
	if err := e.Store.Put(ctx, "tasks", task.ID, task, expected); err != nil {
		t.Fatal(err)
	}
	if err := e.WriteLocalPlan(ctx, project, &task); err != nil {
		t.Fatalf("write plan %s: %v", id, err)
	}
	stored, err := e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(ctx, "tasks", task.ID, task, stored.Version); err != nil {
		t.Fatal(err)
	}
	view, err := e.ReadLocalPlan(ctx, task.ID)
	if err != nil {
		t.Fatalf("read plan %s for human approval: %v", id, err)
	}
	stored, err = e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ReviewLocalPlan(ctx, task.ID, stored.Version, "approve", view.Hash, ""); err != nil {
		t.Fatalf("human approval for plan %s: %v", id, err)
	}
	stored, err = e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := store.Decode[model.Task](stored)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Stage != "coding" || approved.PlanDocument == nil || approved.PlanDocument.ApprovedHash != view.Hash {
		t.Fatalf("plan %s was not frozen by explicit approval: %+v", id, approved)
	}
	return approved
}
