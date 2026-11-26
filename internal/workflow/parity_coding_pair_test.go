package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

func pendingCodingPairFixture(t *testing.T, checks ...model.Check) (*Engine, model.ExecutionPlan) {
	t.Helper()
	e, idea := approvedIdeaPlannerFixture(t, checks...)
	return e, pendingCodingPairForIdea(t, e, idea)
}

func pendingCodingPairForIdea(t *testing.T, e *Engine, idea model.Idea) model.ExecutionPlan {
	t.Helper()
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter { return markdownPlannerAdapter(t, &launches) }
	e.intakeApprovedIdeas(context.Background())
	if err := e.Step(context.Background(), model.PlanID("idea:"+idea.ID)); err != nil {
		t.Fatal(err)
	}
	plan, _ := localStoredTask(t, e, model.PlanID("idea:"+idea.ID))
	return plan
}
func approvedCodingPairFixture(t *testing.T, checks ...model.Check) (*Engine, model.ExecutionPlan) {
	t.Helper()
	e, plan := pendingCodingPairFixture(t, checks...)
	return e, approveCodingPair(t, e, plan)
}

func approveCodingPair(t *testing.T, e *Engine, plan model.ExecutionPlan) model.ExecutionPlan {
	t.Helper()
	_, version := localStoredTask(t, e, plan.ID)
	if err := e.ReviewLocalPlan(context.Background(), plan.ID, version, "approve", plan.PlanDocument.Hash, ""); err != nil {
		t.Fatal(err)
	}
	plan, _ = localStoredTask(t, e, plan.ID)
	return plan
}

func TestParityCodingAndReviewerPreparationStartTogetherWithDurableRuns(t *testing.T) {
	e, plan := approvedCodingPairFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan provider.Input, 4)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
			if err := assertCodingPairCommitted(t, ctx, e.Store, plan.ID); err != nil {
				return provider.Output{}, err
			}
			started <- input
			<-ctx.Done()
			return provider.Output{}, ctx.Err()
		})
	}
	done := make(chan error, 1)
	go func() {
		err := e.Step(ctx, plan.ID)
		if err != nil {
			t.Logf("Step returned: %v", err)
		}
		done <- err
	}()
	t.Cleanup(func() { cancel(); <-done })
	inputs := map[string]provider.Input{}
	for len(inputs) < 2 {
		select {
		case input := <-started:
			inputs[input.Role] = input
		case <-time.After(2 * time.Second):
			t.Fatalf("Coding and Reviewer preparation did not start together: roles=%v", inputs)
		}
	}
	if inputs["review"].Purpose != "review_preparation" || !inputs["review"].ReadOnly || inputs["review"].CWD == inputs["code"].CWD {
		t.Fatalf("Reviewer preparation is not isolated: %+v", inputs)
	}
	assertCodingPairRunIdentities(t, ctx, e.Store, plan.ID)
}

func TestParityCodingPairWaitsWithoutPartialCapacityReservation(t *testing.T) {
	e, plan := approvedCodingPairFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	r, _ := e.Store.Get(ctx, "settings", "global")
	settings := e.settings(ctx)
	settings.GlobalSlots, settings.ProjectSlots = 2, 2
	if err := e.Store.Put(ctx, "settings", "global", settings, r.Version); err != nil {
		t.Fatal(err)
	}
	e.active["p"] = 1
	started := make(chan struct{}, 2)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, _ provider.Input, _ provider.Hooks) (provider.Output, error) {
			started <- struct{}{}
			<-ctx.Done()
			return provider.Output{}, ctx.Err()
		})
	}
	done := make(chan error, 1)
	go func() { done <- e.Step(ctx, plan.ID) }()
	select {
	case <-started:
		cancel()
		<-done
		t.Fatal("partial pair launched with only one available slot")
	case err := <-done:
		cancel()
		if err == nil || !strings.Contains(err.Error(), "pair") {
			t.Fatalf("pair wait not explicit: %v", err)
		}
	case <-time.After(2 * time.Second):
		cancel()
		<-done
		t.Fatal("pair occupied partial reservation while waiting")
	}
	e.mu.Lock()
	active := e.active["p"]
	e.mu.Unlock()
	if active != 1 {
		t.Fatalf("queued pair consumed partial capacity: %d", active)
	}
}

func TestParityReviewerPreparationRejectsChangedBaseSnapshot(t *testing.T) {
	e, plan := approvedCodingPairFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
			if input.Purpose == "review_preparation" {
				if err := os.WriteFile(filepath.Join(input.CWD, "tampered.txt"), []byte("changed"), 0600); err != nil {
					return provider.Output{}, err
				}
				return provider.Output{Text: `{"checklist":[{"requirement_id":"REQ-RESULT","inspection":"Inspect result.txt and configured test evidence."}]}`}, nil
			}
			<-ctx.Done()
			return provider.Output{}, ctx.Err()
		})
	}
	err := e.Step(ctx, plan.ID)
	if err == nil || !strings.Contains(err.Error(), "snapshot") {
		t.Fatalf("altered preparation snapshot accepted: %v", err)
	}
	if _, err := e.Store.Get(context.Background(), "review_preparations", fmt.Sprintf("%s:%d", plan.ID, plan.PlanVersion)); err == nil {
		t.Fatal("altered snapshot published checklist")
	}
}

func TestParityApprovalRejectsSingleAgentSlotConfiguration(t *testing.T) {
	e, idea := approvedIdeaPlannerFixture(t)
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter { return markdownPlannerAdapter(t, &launches) }
	e.intakeApprovedIdeas(context.Background())
	if err := e.Step(context.Background(), model.PlanID("idea:"+idea.ID)); err != nil {
		t.Fatal(err)
	}
	plan, version := localStoredTask(t, e, model.PlanID("idea:"+idea.ID))
	settings := e.settings(context.Background())
	settings.GlobalSlots, settings.ProjectSlots = 1, 1
	r, _ := e.Store.Get(context.Background(), "settings", "global")
	if err := e.Store.Put(context.Background(), "settings", "global", settings, r.Version); err != nil {
		t.Fatal(err)
	}
	err := e.ReviewLocalPlan(context.Background(), plan.ID, version, "approve", plan.PlanDocument.Hash, "")
	if err == nil || !strings.Contains(err.Error(), "two") {
		t.Fatalf("single-slot plan activated: %v", err)
	}
	saved, _ := localStoredTask(t, e, plan.ID)
	if saved.Stage != "plan_approval" {
		t.Fatalf("failed approval changed stage: %s", saved.Stage)
	}
}

func TestParityReviewerPreparationFinishesWhileCodingStillRuns(t *testing.T) {
	e, plan := approvedCodingPairFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 1)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
			if input.Purpose == "review_preparation" {
				return provider.Output{Text: `{"checklist":[{"requirement_id":"REQ-RESULT","inspection":"Inspect result.txt and configured test evidence."}]}`}, nil
			}
			started <- struct{}{}
			<-ctx.Done()
			return provider.Output{}, ctx.Err()
		})
	}
	done := make(chan error, 1)
	go func() { done <- e.Step(ctx, plan.ID) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("Coding never started")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		row, err := e.Store.Get(ctx, "review_preparations", fmt.Sprintf("%s:%d", plan.ID, plan.PlanVersion))
		if err == nil {
			prepared, err := store.Decode[reviewerPreparation](row)
			requireWorkflowSuccess(t, err)
			arow, err := e.Store.Get(ctx, "sessions", prepared.AttemptID)
			requireWorkflowSuccess(t, err)
			attempt, _ := store.Decode[model.Attempt](arow)
			if !attempt.Finished || attempt.Status != "complete" {
				t.Fatalf("preparation left ACP waiting: %+v", attempt)
			}
			e.mu.Lock()
			active := e.active[plan.ProjectID]
			e.mu.Unlock()
			if active != 1 {
				t.Fatalf("completed preparation retained slot: %d", active)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("checklist not durably published: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestParityCompletedPreparationReconcilesAfterLostChecklistPublication(t *testing.T) {
	e, plan := approvedCodingPairFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			if in.Purpose == "review_preparation" {
				return provider.Output{Text: `{"checklist":[{"requirement_id":"REQ-RESULT","inspection":"Check configured result evidence."}]}`}, nil
			}
			<-ctx.Done()
			return provider.Output{}, ctx.Err()
		})
	}
	done := make(chan error, 1)
	go func() { done <- e.Step(ctx, plan.ID) }()
	key := fmt.Sprintf("%s:%d", plan.ID, plan.PlanVersion)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := e.Store.Get(context.Background(), "review_preparations", key); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("preparation did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	// Model loss of only the final checkpoint transaction. The finished ACP row
	// and its output artifact remain durable, as after a commit/storage failure.
	if _, err := e.Store.DB.Exec("DELETE FROM resources WHERE kind='review_preparations' AND id=?", key); err != nil {
		t.Fatal(err)
	}
	replacement := New(e.Store, e.GitHub, e.Root)
	replacement.PlansRoot = e.PlansRoot
	if err := e.Store.RecoverInterrupted(context.Background(), replacement.owner); err != nil {
		t.Fatal(err)
	}
	resumed, cancelResume := context.WithCancel(context.Background())
	defer cancelResume()
	started := make(chan string, 4)
	replacement.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			started <- in.Role
			<-ctx.Done()
			return provider.Output{}, ctx.Err()
		})
	}
	resumedDone := make(chan error, 1)
	go func() { resumedDone <- replacement.Step(resumed, plan.ID) }()
	t.Cleanup(func() { cancelResume(); <-resumedDone })
	select {
	case role := <-started:
		if role != "code" {
			t.Fatalf("completed preparation relaunched: %s", role)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Coding did not resume")
	}
	if _, err := replacement.Store.Get(context.Background(), "review_preparations", key); err != nil {
		t.Fatalf("finished ACP checklist was not reconciled: %v", err)
	}
}

func TestParityTwoCodingReviewerPairsOverlap(t *testing.T) {
	e, first := approvedCodingPairFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	settings := e.settings(ctx)
	settings.GlobalSlots, settings.ProjectSlots = 4, 4
	row, _ := e.Store.Get(ctx, "settings", "global")
	if err := e.Store.Put(ctx, "settings", "global", settings, row.Version); err != nil {
		t.Fatal(err)
	}
	source, _ := e.Store.Get(ctx, "ideas", first.IdeaID)
	idea, _ := store.Decode[model.Idea](source)
	idea.ID, idea.Direction, idea.DecisionStatus, idea.PlanID = "second", "simplify", "draft", ""
	if err := e.Store.Put(ctx, "ideas", idea.ID, idea, 0); err != nil {
		t.Fatal(err)
	}
	idea.DecisionStatus = "approved"
	if err := e.Store.Put(ctx, "ideas", idea.ID, idea, 1); err != nil {
		t.Fatal(err)
	}
	var launches atomic.Int32
	e.Adapter = func(model.Provider) provider.Adapter { return markdownPlannerAdapter(t, &launches) }
	e.intakeApprovedIdeas(ctx)
	if err := e.Step(ctx, model.PlanID("idea:second")); err != nil {
		t.Fatal(err)
	}
	second, version := localStoredTask(t, e, model.PlanID("idea:second"))
	if err := e.ReviewLocalPlan(ctx, second.ID, version, "approve", second.PlanDocument.Hash, ""); err != nil {
		t.Fatal(err)
	}
	started := make(chan provider.Input, 4)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			started <- in
			<-ctx.Done()
			return provider.Output{}, ctx.Err()
		})
	}
	done := make(chan error, 2)
	go func() { done <- e.Step(ctx, first.ID) }()
	go func() { done <- e.Step(ctx, second.ID) }()
	t.Cleanup(func() { cancel(); <-done; <-done })
	roles := map[string]int{}
	for n := 0; n < 4; n++ {
		select {
		case in := <-started:
			roles[in.Role]++
		case <-time.After(5 * time.Second):
			t.Fatalf("two pairs did not overlap: %v", roles)
		}
	}
	if roles["code"] != 2 || roles["review"] != 2 {
		t.Fatalf("two complete pairs required: %v", roles)
	}
	if err := e.Step(ctx, first.ID); !errors.Is(err, store.ErrLeased) {
		t.Fatalf("same plan launched a second pair: %v", err)
	}
}

func TestParityReviewEvaluationIncludesPreparedChecklist(t *testing.T) {
	e, plan := approvedCodingPairFixture(t)
	ctx := context.Background()
	project, err := e.Project(ctx, plan.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.prepareWorkspace(ctx, project, &plan); err != nil {
		t.Fatal(err)
	}
	installNativeParityVerifier(t)
	if err := os.WriteFile(filepath.Join(plan.Worktree, "result.txt"), []byte("reviewed result\n"), 0600); err != nil {
		t.Fatal(err)
	}
	submissionGit(t, plan.Worktree, "add", "result.txt")
	submissionGit(t, plan.Worktree, "commit", "-m", "implement result before review")
	if err := e.verify(ctx, project, &plan); err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("%s:%d", plan.ID, plan.PlanVersion)
	prepared := reviewerPreparation{PlanID: plan.ID, PlanVersion: plan.PlanVersion, Base: plan.Base, DocumentHash: plan.PlanDocument.ApprovedHash, AttemptID: "preparation-attempt", Checklist: []preparationItem{{RequirementID: "REQ-RESULT", Inspection: "Look for exact result file and independent runner evidence."}}}
	if err := e.Store.Put(ctx, "review_preparations", key, prepared, 0); err != nil {
		t.Fatal(err)
	}
	step := &taskStep{Engine: e, ctx: ctx, project: project, task: plan}
	snapshot, err := step.reviewSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot.Prompt, "Look for exact result file and independent runner evidence.") {
		t.Fatal("Reviewer evaluation omitted completed preparation")
	}
}

func putPairReportFixture(t *testing.T, e *Engine, plan model.ExecutionPlan, project model.Project, stage string, alter func(*verification.JobReport)) {
	t.Helper()
	ctx := context.Background()
	commit := strings.TrimSpace(runFixtureCommand(t, ctx, plan.Worktree, "git", "rev-parse", "HEAD"))
	tree := strings.TrimSpace(runFixtureCommand(t, ctx, plan.Worktree, "git", "rev-parse", "HEAD^{tree}"))
	manifest := verification.JobManifest{Version: 1, JobID: plan.VerificationID, PlanID: plan.ID, PlanVersion: plan.PlanVersion, PlanHash: plan.PlanDocument.ApprovedHash, ConfigurationHash: verificationConfigurationHash(plan), CandidateCommit: commit, CandidateTree: tree, BaseCommit: plan.Base, Commands: []verification.JobCommand{{Name: "test", Command: []string{"sh", "-c", "test -f result.txt"}, TimeoutSeconds: 600}}}
	zero := 0
	report := verification.JobReport{ManifestHash: manifest.Digest(), PlanID: plan.ID, PlanVersion: plan.PlanVersion, PlanHash: manifest.PlanHash, ConfigurationHash: manifest.ConfigurationHash, CandidateCommit: commit, CandidateTree: tree, Passed: true, Results: []verification.JobResult{{Result: verification.Result{Name: "test", Command: []string{"sh", "-c", "test -f result.txt"}, ExitCode: &zero, Passed: true, Runtime: verification.RuntimeIdentity{CleanupVerified: true}}, Status: "pass", ManifestHash: manifest.Digest(), ConfigurationHash: manifest.ConfigurationHash, CandidateCommit: commit, CandidateTree: tree}}}
	if alter != nil {
		alter(&report)
	}
	if stage == "" {
		stage = "complete"
	}
	value := map[string]any{"stage": stage, "provenance": model.Provenance{Task: plan.ID, PlanVersion: plan.PlanVersion, Head: commit, Base: plan.Base, PolicyHash: project.PolicyHash, VerificationID: plan.VerificationID}, "runner_manifest": manifest, "runner_report": report}
	if err := e.Store.Put(ctx, "verification", plan.VerificationID, value, 0); err != nil {
		t.Fatal(err)
	}
}

func TestParityReviewEvaluationRejectsIncompleteOrMismatchedReport(t *testing.T) {
	for _, test := range []struct {
		name, stage string
		alter       func(*verification.JobReport)
	}{
		{name: "unfinished", stage: "running"},
		{name: "missing command", alter: func(r *verification.JobReport) { r.Results = nil }},
		{name: "different candidate", alter: func(r *verification.JobReport) { r.CandidateCommit = strings.Repeat("a", 40) }},
		{name: "different approval", alter: func(r *verification.JobReport) { r.PlanVersion++ }},
		{name: "different configuration", alter: func(r *verification.JobReport) { r.ConfigurationHash = "other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			e, plan := approvedCodingPairFixture(t)
			ctx := context.Background()
			project, err := e.Project(ctx, plan.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			if err := e.prepareWorkspace(ctx, project, &plan); err != nil {
				t.Fatal(err)
			}
			plan.VerificationID = "report-identity-test"
			putPairReportFixture(t, e, plan, project, test.stage, test.alter)
			step := &taskStep{Engine: e, ctx: ctx, project: project, task: plan}
			// A missing preparation would also block dispatch, so install valid preparation
			// and require the report error specifically.
			prepared := reviewerPreparation{PlanID: plan.ID, PlanVersion: plan.PlanVersion, Base: plan.Base, DocumentHash: plan.PlanDocument.ApprovedHash, Checklist: []preparationItem{{RequirementID: "REQ-RESULT", Inspection: "Inspect file."}}}
			if err := e.Store.Put(ctx, "review_preparations", fmt.Sprintf("%s:%d", plan.ID, plan.PlanVersion), prepared, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := step.reviewSnapshot(); err == nil || !strings.Contains(err.Error(), "report") {
				t.Fatalf("invalid complete-report handoff accepted: %v", err)
			}
		})
	}
}

func assertCodingPairCommitted(t *testing.T, ctx context.Context, database *store.Store, planID string) error {
	t.Helper()
	rows, err := database.List(ctx, "sessions")
	if err != nil {
		return err
	}
	pairRows := 0
	for _, row := range rows {
		attempt, _ := store.Decode[model.Attempt](row)
		if attempt.Task == planID && (attempt.Role == "code" || attempt.Purpose == "review_preparation") {
			pairRows++
		}
	}
	if pairRows != 2 {
		t.Errorf("adapter launched before both ACP rows committed: %d", pairRows)
	}
	return nil
}

func assertCodingPairRunIdentities(t *testing.T, ctx context.Context, database *store.Store, planID string) {
	t.Helper()
	rows, err := database.List(ctx, "sessions")
	requireWorkflowSuccess(t, err)
	roles := map[string]string{}
	for _, row := range rows {
		a, _ := store.Decode[model.Attempt](row)
		if a.Task == planID && (a.Role == "code" || a.Purpose == "review_preparation") {
			roles[a.Role] = a.AttemptID
		}
	}
	if roles["code"] == "" || roles["review"] == "" || roles["code"] == roles["review"] {
		t.Fatalf("pair lacks independent persisted ACP run identities: %v", roles)
	}
}
