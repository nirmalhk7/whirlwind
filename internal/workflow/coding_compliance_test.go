package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

func TestCodingUnsupportedVerifierSandboxPausesApprovedPlan(t *testing.T) {
	e, plan := approvedCodingPairFixture(t)
	t.Setenv("WHIRLWIND_VERIFY_BIN", "/usr/bin/true")
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, input provider.Input, hooks provider.Hooks) (provider.Output, error) {
			if input.Purpose == "review_preparation" {
				return provider.Output{Text: codingFixtureChecklist}, nil
			}
			return fakeAgent{}.Run(ctx, input, hooks)
		})
	}
	e.dispatch(context.Background())
	e.background.Wait()
	saved, _ := localStoredTask(t, e, plan.ID)
	if saved.Stage != "paused" || saved.ResumeStage != "coding" || !strings.Contains(saved.Error, "canonical local verification requires native nono") || saved.ChecksPassed {
		t.Fatalf("unsupported verifier sandbox did not pause the approved candidate: stage=%s resume=%s error=%q", saved.Stage, saved.ResumeStage, saved.Error)
	}
}

const codingFixtureChecklist = `{"checklist":[{"requirement_id":"REQ-RESULT","inspection":"Inspect result.txt and the configured check evidence."}]}`

func TestCodingMissingVerifierIsAnActionableSetupError(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip(runnerNativeAcceptanceLabel)
	}
	e, plan := approvedCodingPairFixture(t)
	ctx := context.Background()
	project, err := e.Project(ctx, plan.ProjectID)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, e.prepareWorkspace(ctx, project, &plan))
	t.Setenv("WHIRLWIND_EXECUTION_LAUNCHER", "")
	t.Setenv("WHIRLWIND_VERIFY_BIN", filepath.Join(t.TempDir(), "missing-verifier"))
	err = e.finishCode(ctx, project, &plan)
	if !errors.Is(err, verification.ErrJobSetup) || !strings.Contains(err.Error(), "missing-verifier") || plan.Stage != "coding" {
		t.Fatalf("missing verifier should keep the plan resumable at coding: stage=%s error=%v", plan.Stage, err)
	}
}

func TestCodingCompetingSupervisorsClaimOnlyOneApprovedRun(t *testing.T) {
	e, plan := approvedCodingPairFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	started := make(chan struct{}, 2)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
			if input.Role == "review" {
				return provider.Output{Text: codingFixtureChecklist}, nil
			}
			started <- struct{}{}
			<-ctx.Done()
			return provider.Output{}, ctx.Err()
		})
	}
	second := New(e.Store, e.GitHub, e.Root)
	second.Adapter = e.Adapter
	e.dispatch(ctx)
	defer func() { cancel(); e.background.Wait() }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("first approved Coding claim never reached the worker")
	}
	second.dispatch(ctx)
	second.background.Wait()
	if len(started) != 0 || len(nativeCodingAttempts(t, e, plan)) != 1 {
		t.Fatal("concurrent supervisor started a second Coding attempt from the same approval")
	}
}

func TestCodingClarificationPausesDispatchAndResumesFrozenVersion(t *testing.T) {
	e, plan := approvedCodingPairFixture(t)
	ctx := context.Background()
	t.Setenv("WHIRLWIND_VERIFY_BIN", "/usr/bin/true")
	calls := 0
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, input provider.Input, hooks provider.Hooks) (provider.Output, error) {
			if input.Role == "review" {
				return provider.Output{Text: codingFixtureChecklist}, nil
			}
			calls++
			if calls == 1 {
				_, err := hooks.Questions(ctx, []model.Question{{ID: "format", Text: "What output format should the result use?"}})
				return provider.Output{}, err
			}
			if !strings.Contains(input.Prompt, "plain text") {
				return provider.Output{}, errors.New("Coding resumed without the recorded answer")
			}
			return fakeAgent{}.Run(ctx, input, hooks)
		})
	}
	e.dispatch(ctx)
	e.background.Wait()
	paused, _ := localStoredTask(t, e, plan.ID)
	if paused.Stage != "paused" || paused.Wait == nil || paused.Wait.Kind != "human_decision" {
		t.Fatalf("Coding clarification did not pause the plan: stage=%s error=%q", paused.Stage, paused.Error)
	}
	e.dispatch(ctx)
	e.background.Wait()
	if calls != 1 {
		t.Fatalf("pending clarification relaunched Coding %d times", calls)
	}
	questions, err := e.Store.List(ctx, "questions")
	requireWorkflowSuccess(t, err)
	if len(questions) != 1 {
		t.Fatalf("expected one persisted question, got %d", len(questions))
	}
	requireWorkflowSuccess(t, e.AnswerQuestionSet(ctx, questions[0].ID, questions[0].Version, map[string]string{"format": "plain text"}, false))
	e.dispatch(ctx)
	e.background.Wait()
	resumed, _ := localStoredTask(t, e, plan.ID)
	attempts := nativeCodingAttempts(t, e, plan)
	if calls != 2 || len(attempts) != 2 || attempts[1].Success == nil || !*attempts[1].Success ||
		resumed.PlanVersion != plan.PlanVersion || resumed.PlanDocument.ApprovedHash != plan.PlanDocument.ApprovedHash {
		t.Fatalf("answer did not resume the frozen Coding version: stage=%s calls=%d attempts=%v", resumed.Stage, calls, nativeCodingFailures(attempts))
	}
}

func TestCodingRepairCannotWriteOutsideApprovedUnits(t *testing.T) {
	e, plan := approvedCodingPairFixture(t)
	ctx := context.Background()
	project, err := e.Project(ctx, plan.ProjectID)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, e.prepareWorkspace(ctx, project, &plan))
	plan.Stage = "repair"
	record, err := e.Store.Get(ctx, "execution_plans", plan.ID)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, e.Store.Put(ctx, "execution_plans", plan.ID, plan, record.Version))
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
			if input.Execution == nil {
				return provider.Output{}, errors.New("repair has no execution profile")
			}
			if !slices.Equal(input.Execution.AllowedPaths, []string{resultFile}) {
				t.Errorf("repair profile did not retain the approved unit scope: %v", input.Execution.AllowedPaths)
			}
			err := os.WriteFile(filepath.Join(input.CWD, "outside-plan.txt"), []byte("discard this candidate only"), 0600)
			return provider.Output{Text: "Repaired"}, err
		})
	}
	before := submissionGit(t, plan.Worktree, gitRevParseCommand, "HEAD")
	if err := e.Step(ctx, plan.ID); err == nil || !strings.Contains(err.Error(), "out-of-scope file: outside-plan.txt") {
		t.Fatalf("supervisor did not reject an out-of-scope repair: %v", err)
	}
	if head := submissionGit(t, plan.Worktree, gitRevParseCommand, "HEAD"); head != before {
		t.Fatalf("out-of-scope repair advanced HEAD from %s to %s", before, head)
	}
}

func TestCodingCommitSkipsRepositoryHookAndPreservesPathBytes(t *testing.T) {
	e, task, repo := testEngine(t)
	ctx := context.Background()
	project, err := e.Project(ctx, task.ProjectID)
	requireWorkflowSuccess(t, err)
	const name = "new file.txt"
	project.Policy.AllowedPaths = []string{name}
	requireWorkflowSuccess(t, e.prepareWorkspace(ctx, project, &task))
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	submissionGit(t, repo, "config", "core.hooksPath", filepath.Dir(hook))
	requireWorkflowSuccess(t, os.WriteFile(hook, []byte("#!/bin/sh\ntouch hook-ran\n"), 0700))
	requireWorkflowSuccess(t, os.WriteFile(filepath.Join(task.Worktree, name), []byte("candidate\n"), 0600))
	requireWorkflowSuccess(t, e.commit(ctx, project, task.Worktree, "Candidate"))
	if got := submissionGit(t, task.Worktree, "show", "HEAD:"+name); got != "candidate" {
		t.Fatalf("candidate contents = %q", got)
	}
	if _, err := os.Stat(filepath.Join(task.Worktree, "hook-ran")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repository hook ran on the supervisor: %v", err)
	}
}

func TestCodingConflictResumesAndSupervisorCommitsScopedResolution(t *testing.T) {
	e, task, project, unit := codingConflictFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var launches int
	e.Adapter = conflictRecoveryAdapter(t, cancel, &launches)
	if _, err := e.mergeUnit(ctx, project, &task, &unit); !errors.Is(err, context.Canceled) {
		t.Fatalf("interruption did not stop conflict resolution: %v", err)
	}
	ctx = context.Background()
	hook := filepath.Join(project.Path, ".git", "hooks", "post-commit")
	submissionGit(t, project.Path, "config", "core.hooksPath", filepath.Dir(hook))
	requireWorkflowSuccess(t, os.WriteFile(hook, []byte("#!/bin/sh\ntouch hook-ran\n"), 0700))
	restarted := New(e.Store, e.GitHub, e.Root)
	restarted.Adapter = e.Adapter
	_, err := restarted.mergeUnit(ctx, project, &task, &unit)
	requireWorkflowSuccess(t, err)
	resolved := submissionGit(t, task.Worktree, gitRevParseCommand, "HEAD")
	if result := submissionGit(t, task.Worktree, "show", "HEAD:"+resultFile); result != "keep both changes" {
		t.Fatalf("supervisor did not commit the resolution: %q", result)
	}
	if got := submissionGit(t, task.Worktree, "status", "--porcelain"); got != "" {
		t.Fatalf("integration did not finish cleanly: %q", got)
	}
	// Simulate losing only the database publication after Git completed.
	key := integrationCheckpointID(task, unit.ID, unit.Commit)
	row, err := e.Store.Get(ctx, "checkpoints", key)
	requireWorkflowSuccess(t, err)
	checkpoint, err := store.Decode[integrationCheckpoint](row)
	requireWorkflowSuccess(t, err)
	checkpoint.Commit = ""
	requireWorkflowSuccess(t, e.Store.Put(ctx, "checkpoints", key, checkpoint, row.Version))
	_, err = restarted.mergeUnit(ctx, project, &task, &unit)
	requireWorkflowSuccess(t, err)
	if launches != 2 || submissionGit(t, task.Worktree, gitRevParseCommand, "HEAD") != resolved {
		t.Fatal("resumed integration replayed a completed resolution")
	}
	row, err = e.Store.Get(ctx, "checkpoints", key)
	requireWorkflowSuccess(t, err)
	checkpoint, err = store.Decode[integrationCheckpoint](row)
	requireWorkflowSuccess(t, err)
	if checkpoint.Commit != resolved || checkpoint.Tree != submissionGit(t, task.Worktree, gitRevParseCommand, "HEAD^{tree}") {
		t.Fatalf("resolved candidate %s was not recovered with exact tree evidence", resolved)
	}
}

func conflictRecoveryAdapter(t *testing.T, cancel context.CancelFunc, launches *int) func(model.Provider) provider.Adapter {
	t.Helper()
	return func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
			*launches++
			if input.Execution == nil {
				return provider.Output{}, errors.New("conflict has no execution profile")
			}
			if !slices.Equal(input.Execution.AllowedPaths, []string{resultFile}) {
				t.Errorf("conflict profile must grant only the conflicted file: %v", input.Execution.AllowedPaths)
			}
			if !strings.Contains(input.Prompt, resultFile) || strings.Contains(input.Prompt, "git cherry-pick --continue") {
				t.Errorf("conflict prompt did not leave Git continuation to the supervisor: %q", input.Prompt)
			}
			if *launches == 1 {
				cancel()
				return provider.Output{}, context.Canceled
			}
			err := os.WriteFile(filepath.Join(input.CWD, resultFile), []byte("keep both changes\n"), 0600)
			return provider.Output{Text: "Resolved in the worktree"}, err
		})
	}
}

func codingConflictFixture(t *testing.T) (*Engine, model.ExecutionPlan, model.Project, model.Unit) {
	return codingConflictFixtureFor(t, resultFile)
}

func TestCodingConflictNeverConvertsFilenameIntoWriteGlob(t *testing.T) {
	e, task, project, unit := codingConflictFixtureFor(t, "conflict?.txt")
	unit.AllowedPaths = []string{"**"}
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			t.Error("Coding launched with an ambiguous conflict filename in its write grant")
			return provider.Output{}, context.Canceled
		})
	}
	_, err := e.mergeUnit(context.Background(), project, &task, &unit)
	if err == nil || !strings.Contains(err.Error(), "literal conflict path") {
		t.Fatalf("ambiguous conflict filename did not fail closed: %v", err)
	}
}

func codingConflictFixtureFor(t *testing.T, name string) (*Engine, model.ExecutionPlan, model.Project, model.Unit) {
	t.Helper()
	e, task, repo := testEngine(t)
	ctx := context.Background()
	requireWorkflowSuccess(t, os.WriteFile(filepath.Join(repo, name), []byte("base\n"), 0600))
	submissionGit(t, repo, "--literal-pathspecs", "add", "--", name)
	submissionGit(t, repo, "commit", "-m", "Base content")
	project, err := e.Project(ctx, task.ProjectID)
	requireWorkflowSuccess(t, err)
	requireWorkflowSuccess(t, e.prepareWorkspace(ctx, project, &task))
	unit := model.Unit{ID: "resolution", Objective: "Keep both edits", Base: task.Base, AllowedPaths: []string{name, "ordinary.txt"}}
	unit.Branch, unit.Worktree = unitWorktreeIdentity(task, unit.ID)
	requireWorkflowSuccess(t, e.worktree(ctx, project.Path, unit.Worktree, unit.Branch, unit.Base))
	for _, item := range []struct{ root, content string }{{task.Worktree, "integration\n"}, {unit.Worktree, "unit\n"}} {
		requireWorkflowSuccess(t, os.WriteFile(filepath.Join(item.root, name), []byte(item.content), 0600))
		submissionGit(t, item.root, "--literal-pathspecs", "add", "--", name)
		submissionGit(t, item.root, "commit", "-m", fmt.Sprintf("Result in %s", filepath.Base(item.root)))
	}
	unit.Commit = submissionGit(t, unit.Worktree, gitRevParseCommand, "HEAD")
	return e, task, project, unit
}
