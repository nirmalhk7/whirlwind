package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

// TestInterruptedIntegrationUsesDurableUnitCheckpoints verifies a restarted integrator reuses durable checkpoints without launching duplicate code attempts or commits.
func TestInterruptedIntegrationUsesDurableUnitCheckpoints(t *testing.T) {
	ctx := context.Background()
	e, task, project, ready := approvedUnitCheckpointFixture(t)
	project.Policy.RunTimeoutSeconds = 60
	e.Adapter = func(model.Provider) provider.Adapter { return unitAgent{} }
	if err := e.prepareWorkspace(ctx, project, &task); err != nil {
		t.Fatal(err)
	}
	if err := e.runUnits(ctx, project, &task, ready); err != nil {
		t.Fatalf("create durable unit checkpoints: %v", err)
	}
	resumedIndex := ready[1]
	resumedUnit := task.Plan.Units[resumedIndex]
	originalBase := resumedUnit.Base
	resumedFile := filepath.Join(resumedUnit.Worktree, filepath.Base(resumedUnit.Worktree)+"-one.txt")
	if err := os.WriteFile(resumedFile, []byte("preserve this uncommitted work"), 0600); err != nil {
		t.Fatal(err)
	}
	unitRecordKey := unitKey(task.ID, task.PlanVersion, resumedUnit.ID)
	unitRecord, err := e.Store.Get(ctx, "units", unitRecordKey)
	if err != nil {
		t.Fatal(err)
	}
	unfinished, err := store.Decode[model.Unit](unitRecord)
	if err != nil {
		t.Fatal(err)
	}
	unfinished.Stage = "running"
	unfinished.Commit = ""
	if err := e.Store.Put(ctx, "units", unitRecordKey, unfinished, unitRecord.Version); err != nil {
		t.Fatal(err)
	}
	completed := map[string]bool{}
	if len(ready) < 2 {
		t.Fatalf("fixture needs independent units for an interrupted integration: %v", ready)
	}
	if err := e.integrateUnits(ctx, project, &task, ready[:1], completed); err != nil {
		t.Fatalf("integrate first unit before simulated interruption: %v", err)
	}
	if task.Plan.Units[ready[1]].Stage != "complete" {
		t.Fatalf("second unit checkpoint was not left complete for restart: %+v", task.Plan.Units[ready[1]])
	}

	latest, err := e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(e.Store, e.GitHub, e.Root)
	restarted.PlansRoot = e.PlansRoot
	resumedDirtyWork := false
	restarted.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			resumedFile := filepath.Join(in.CWD, filepath.Base(in.CWD)+"-one.txt")
			body, readErr := os.ReadFile(resumedFile)
			actualWorktree, actualErr := canonicalPath(in.CWD)
			wantWorktree, wantErr := canonicalPath(resumedUnit.Worktree)
			if readErr != nil || actualErr != nil || wantErr != nil || string(body) != "preserve this uncommitted work" || actualWorktree != wantWorktree {
				return provider.Output{}, fmt.Errorf("durable unit worktree did not preserve the interrupted file: cwd=%q want=%q body=%q err=%v", in.CWD, resumedUnit.Worktree, body, readErr)
			}
			resumedDirtyWork = true
			if writeErr := os.WriteFile(resumedFile, []byte("continued after restart"), 0600); writeErr != nil {
				return provider.Output{}, writeErr
			}
			return provider.Output{Text: "continued"}, nil
		})
	}
	resumed, err := store.Decode[model.Task](latest)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.prepareWorkspace(ctx, project, &resumed); err != nil {
		t.Fatalf("restore task worktree: %v", err)
	}
	completed = restarted.restoreUnits(ctx, &resumed)
	remaining := readyUnits(resumed.Plan.Units, completed)
	if len(completed) != 1 || len(remaining) != 1 || remaining[0] != resumedIndex {
		t.Fatalf("restart did not restore the partial integration boundary: ready=%v completed=%v", remaining, completed)
	}
	if err := restarted.runUnits(ctx, project, &resumed, remaining); err != nil {
		t.Fatalf("resume the unintegrated durable unit checkpoint: %v", err)
	}
	if err := restarted.integrateUnits(ctx, project, &resumed, remaining, completed); err != nil {
		t.Fatalf("finish integration from durable unit checkpoints: %v", err)
	}
	if !resumedDirtyWork || resumed.Plan.Units[resumedIndex].Base != originalBase {
		t.Fatalf("restart did not preserve dirty unit work and its original base: ran=%v base=%q want=%q", resumedDirtyWork, resumed.Plan.Units[resumedIndex].Base, originalBase)
	}
	if body, err := os.ReadFile(resumedFile); err != nil || string(body) != "continued after restart" {
		t.Fatalf("resumed integration lost the unfinished unit file: body=%q err=%v", body, err)
	}
	headBeforeResume := submissionGit(t, resumed.Worktree, "rev-parse", "HEAD")
	countBeforeResume := submissionGit(t, resumed.Worktree, "rev-list", "--count", resumed.Base+"..HEAD")
	validateIntegratedUnitCommits(t, resumed)

	if err := restarted.runUnits(ctx, project, &resumed, nil); err != nil {
		t.Fatalf("replay coding with completed unit checkpoints: %v", err)
	}
	if err := restarted.integrateUnits(ctx, project, &resumed, nil, completed); err != nil {
		t.Fatalf("replay integration with completed unit checkpoints: %v", err)
	}
	if got := submissionGit(t, resumed.Worktree, "rev-parse", "HEAD"); got != headBeforeResume {
		t.Fatalf("checkpoint replay duplicated or changed commits: before=%s after=%s", headBeforeResume, got)
	}
	if got := submissionGit(t, resumed.Worktree, "rev-list", "--count", resumed.Base+"..HEAD"); got != countBeforeResume {
		t.Fatalf("checkpoint replay duplicated commits: before=%s after=%s", countBeforeResume, got)
	}
	if resumed.ChecksPassed || resumed.Stage != "coding" {
		t.Fatalf("unit checkpoint replay incorrectly bypassed mandatory verification: %+v", resumed)
	}
	for _, name := range []string{"a-one.txt", "a-two.txt", "b-one.txt", "b-two.txt"} {
		if _, err := os.Stat(filepath.Join(resumed.Worktree, name)); err != nil {
			t.Fatalf("resumed integration lost unit file %s: %v", name, err)
		}
	}
	checkpointKey := unitKey(resumed.ID, resumed.PlanVersion, resumed.Plan.Units[0].ID)
	checkpoint, err := restarted.Store.Get(ctx, "units", checkpointKey)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Decode[model.Unit](checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	saved.Worktree = filepath.Join(filepath.Dir(saved.Worktree), "foreign-worktree")
	if err := restarted.Store.Put(ctx, "units", checkpointKey, saved, checkpoint.Version); err != nil {
		t.Fatal(err)
	}
	if err := restarted.executeUnits(ctx, project, &resumed); err == nil || !strings.Contains(err.Error(), "validate integrated unit") {
		t.Fatal("execution trusted an integrated checkpoint that points outside its owned worktree")
	}
	newEngine, newTask, newProject, newReady := approvedUnitCheckpointFixture(t)
	if err := newEngine.prepareWorkspace(ctx, newProject, &newTask); err != nil {
		t.Fatal(err)
	}
	newUnit := &newTask.Plan.Units[newReady[0]]
	newUnit.Base = "untrusted-planner-base"
	newUnit.Stage = "integrated"
	newUnit.Worktree = filepath.Join(newEngine.Root, "foreign-worktree")
	newUnit.Branch = "foreign-branch"
	newUnit.Commit = "untrusted-planner-commit"
	run := &unitRun{Engine: newEngine, ctx: ctx, project: newProject, task: newTask, unit: newUnit}
	if restored, err := run.restoreCheckpoint(); err != nil || restored {
		t.Fatalf("absent durable checkpoint restored planner runtime claims: restored=%v err=%v", restored, err)
	}
	if newUnit.Base != "" || newUnit.Stage != "" || newUnit.Worktree != "" || newUnit.Branch != "" || newUnit.Commit != "" {
		t.Fatalf("untrusted planner runtime metadata survived without a durable checkpoint: %+v", newUnit)
	}
	if err := run.prepareWorktree(); err != nil {
		t.Fatalf("prepare initial unit from the task candidate head: %v", err)
	}
	if got, want := newUnit.Base, submissionGit(t, newTask.Worktree, "rev-parse", "HEAD"); got != want {
		t.Fatalf("new unit trusted planner base: got=%s want candidate head=%s", got, want)
	}
}

type waitingAgent struct{}

// Run blocks until context cancellation and returns its error to exercise run timeouts.
func (waitingAgent) Run(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
	<-ctx.Done()
	return provider.Output{}, ctx.Err()
}

// TestConfiguredRunBudgetStopsAttempt verifies an explicit project run timeout cancels an agent attempt.
func TestConfiguredRunBudgetStopsAttempt(t *testing.T) {
	e, task, repo := testEngine(t)
	p, _ := e.Project(context.Background(), task.ProjectID)
	p.Policy.RunTimeoutSeconds = 1
	e.Adapter = func(model.Provider) provider.Adapter { return waitingAgent{} }
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	start := time.Now()
	_, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: repo, Prompt: "test", ReadOnly: false, SessionID: ""})
	if !errors.Is(err, store.ErrBudgetExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("run budget not enforced: %v %s", err, time.Since(start))
	}
	assertOnlyAttemptStatus(t, e, "failed")
	usage, usageErr := e.Store.TaskUsage(context.Background(), task.ID)
	if usageErr != nil || usage.ActiveAttempts != 0 || usage.ReservedRuntimeSeconds != 0 {
		t.Fatalf("budget failure left active or reserved execution: %+v %v", usage, usageErr)
	}
}
