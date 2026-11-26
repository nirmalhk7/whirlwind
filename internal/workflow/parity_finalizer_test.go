package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

const (
	finalizerFixtureRevParse    = "rev-parse"
	finalizerFixtureResultPath  = "result.txt"
	finalizerFixtureHeadsPrefix = "refs/heads/"
	finalizerFixtureTreeSuffix  = "^{tree}"
)

// The real standalone runner's durable report must be usable by acceptance;
// replacing it with legacy in-process runtime rows would hide this handoff bug.
func acceptedParityDeliveryFixture(t *testing.T) (*Engine, model.ExecutionPlan, model.Project, model.Record) {
	t.Helper()
	e, idea := approvedIdeaPlannerFixture(t)
	installNativeParityVerifier(t)
	configureQualifiedFixtureChecks(t, e, idea.ProjectID, []model.Check{cleanQltyFixtureCheck(t)})
	plan := approveCodingPair(t, e, pendingCodingPairForIdea(t, e, idea))
	ctx := context.Background()
	project, err := e.Project(ctx, plan.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	plan.Worktree = project.Path
	plan.Base = submissionGit(t, project.Path, finalizerFixtureRevParse, "HEAD")
	if err := os.WriteFile(filepath.Join(project.Path, finalizerFixtureResultPath), []byte("accepted result\n"), 0600); err != nil {
		t.Fatal(err)
	}
	submissionGit(t, project.Path, "add", finalizerFixtureResultPath)
	submissionGit(t, project.Path, "commit", "-m", "tested code version")
	plan.ReviewedCommit = submissionGit(t, project.Path, finalizerFixtureRevParse, "HEAD")
	if err := e.verify(ctx, project, &plan); err != nil {
		t.Fatal(err)
	}
	plan.Stage, plan.ReviewID, plan.ChecksPassed = "submitting", plan.ID+":accepted-review", true
	provenance := acceptedReviewProvenance(plan, project.PolicyHash)
	review := model.Review{Provenance: &provenance, Requirements: []model.RequirementResult{{ID: "REQ-RESULT", Status: "pass", Code: []string{finalizerFixtureResultPath}, Evidence: []string{"Standalone runner passed approved test."}, Explanation: "Required result exists."}}}
	if err := e.Store.Put(ctx, "reviews", plan.ReviewID, review, 0); err != nil {
		t.Fatal(err)
	}
	_, version := localStoredTask(t, e, plan.ID)
	if err := e.Store.Put(ctx, "execution_plans", plan.ID, plan, version); err != nil {
		t.Fatal(err)
	}
	record, err := e.Store.Get(ctx, "execution_plans", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	return e, plan, project, record
}

func TestParityFinalizerAcceptsStandaloneVerificationEvidence(t *testing.T) {
	e, plan, _, _ := acceptedParityDeliveryFixture(t)
	_, report, accepted, err := e.LoadAcceptedReviewReport(context.Background(), plan.ID)
	if err != nil || !accepted || report.Score != 100 {
		t.Fatalf("standalone evidence cannot reach finalization: accepted=%v score=%v err=%v", accepted, report.Score, err)
	}
}

func TestParityFinalizerDoesNotPublishBeforeScopedOperation(t *testing.T) {
	e, plan, project, record := acceptedParityDeliveryFixture(t)
	step := taskStep{Engine: e, ctx: context.Background(), task: plan, project: project, record: record}
	for n := 0; n < 2; n++ {
		if _, err := step.submitting(); err != nil {
			t.Fatal(err)
		}
		if err := e.Store.Put(step.ctx, "execution_plans", plan.ID, step.task, step.record.Version); err != nil {
			t.Fatal(err)
		}
		step.record, _ = e.Store.Get(step.ctx, "execution_plans", plan.ID)
	}
	if _, err := submissionGitRun(step.ctx, project.Path, finalizerFixtureRevParse, "--verify", finalizerFixtureHeadsPrefix+step.task.Delivery.Branch); err == nil {
		t.Fatal("delivery branch published before Finalizer invoked scoped operation")
	}
	if step.task.Delivery.Worktree == project.Path || submissionGit(t, step.task.Delivery.Worktree, finalizerFixtureRevParse, "HEAD^{tree}") != step.task.Delivery.Tree {
		t.Fatal("Finalizer lacks independent accepted-tree snapshot")
	}
}

func TestParityFinalizerSummaryAloneCannotCompleteDelivery(t *testing.T) {
	e, plan, project, record := acceptedParityDeliveryFixture(t)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
			if err := runNativeFinalizerPeer(ctx, input); err != nil {
				return provider.Output{}, err
			}
			return provider.Output{Text: "Delivered everything."}, nil
		})
	}
	step := taskStep{Engine: e, ctx: context.Background(), task: plan, project: project, record: record}
	var err error
	for n := 0; n < 3; n++ {
		var finished bool
		finished, err = step.submitting()
		if err != nil || finished {
			break
		}
		if saveErr := e.Store.Put(step.ctx, "execution_plans", plan.ID, step.task, step.record.Version); saveErr != nil {
			t.Fatal(saveErr)
		}
		step.record, _ = e.Store.Get(step.ctx, "execution_plans", plan.ID)
	}
	if err == nil || !strings.Contains(err.Error(), "finalization operation") || step.task.Stage == "complete" {
		t.Fatalf("summary bypassed scoped finalization: stage=%s err=%v", step.task.Stage, err)
	}
}

func runNativeFinalizerPeer(ctx context.Context, input provider.Input) error {
	rpc, err := provider.StartWithProfile(ctx, []string{"/bin/sh", "-c", `IFS= read -r request; id=$(printf '%s' "$request" | sed -E 's/.*"id":([^,}]+).*/\1/'); printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"`}, *input.Execution, nil)
	if err != nil {
		return err
	}
	if err = rpc.Call(ctx, "initialize", map[string]any{"protocolVersion": 1}, nil); err != nil {
		_ = rpc.Close()
		return err
	}
	return rpc.Close()
}

func TestParityFinalizerScopedOperationPublishesAcceptedTreeIdempotently(t *testing.T) {
	e, plan, project, _ := acceptedParityDeliveryFixture(t)
	var delivered provider.FinalizationResult
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
			return runFinalizerIdempotenceAcceptance(ctx, in, h, project.Path, &delivered)
		})
	}
	for n := 0; n < 3; n++ {
		if err := e.Step(context.Background(), plan.ID); err != nil {
			var lease store.Lease
			leaseErr := e.Store.DB.QueryRow("SELECT owner,fence,expires FROM leases WHERE id=?", "task:"+plan.ID).Scan(&lease.Owner, &lease.Fence, &lease.ExpiresAt)
			current, _ := localStoredTask(t, e, plan.ID)
			t.Fatalf("finalization transition %d failed at %s: %v; lease=%+v read=%v", n, current.Stage, err, lease, leaseErr)
		}
	}
	saved, _ := localStoredTask(t, e, plan.ID)
	if saved.Stage != "complete" || delivered.Commit == "" || delivered.Branch == "" {
		t.Fatalf("trusted operation did not complete: %+v %+v", saved.Delivery, delivered)
	}
	if !strings.HasPrefix(delivered.Branch, "codex/delivery/plan-") {
		t.Fatalf("new delivery branch retains obsolete task terminology: %s", delivered.Branch)
	}
	if delivered.Commit == plan.ReviewedCommit {
		t.Fatal("Finalizer did not create a provenance-bound local delivery commit")
	}
	message := submissionGit(t, project.Path, "show", "-s", "--format=%B", delivered.Commit)
	for _, value := range []string{"Whirlwind local delivery", plan.ID, plan.ReviewedCommit, plan.PlanDocument.ApprovedHash, plan.VerificationID, plan.ReviewID} {
		if !strings.Contains(message, value) {
			t.Fatalf("delivery commit lacks accepted provenance %q: %s", value, message)
		}
	}
	if delivered.Tree != submissionGit(t, project.Path, finalizerFixtureRevParse, plan.ReviewedCommit+finalizerFixtureTreeSuffix) || submissionGit(t, project.Path, finalizerFixtureRevParse, finalizerFixtureHeadsPrefix+delivered.Branch) != delivered.Commit || submissionGit(t, project.Path, finalizerFixtureRevParse, delivered.Commit+finalizerFixtureTreeSuffix) != delivered.Tree {
		t.Fatal("delivery differs from accepted tree")
	}
	if submissionGit(t, project.Path, finalizerFixtureRevParse, "HEAD") != plan.ReviewedCommit {
		t.Fatal("trusted operation changed Coding checkout")
	}
	var count int
	if err := e.Store.DB.QueryRow("SELECT count(*) FROM resources WHERE kind='finalizations'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("finalization not durably idempotent: count=%d err=%v", count, err)
	}
}

func TestParityFinalizerRejectsChangedWorktreeAndRevokedPlan(t *testing.T) {
	for _, mutation := range []string{"changed source", "revoked plan", "saved delivery tree"} {
		t.Run(mutation, func(t *testing.T) {
			assertFinalizerRejectsMutation(t, mutation)
		})
	}
}

func TestParityFinalizerRecoversPublishedReferenceAfterLostCompletion(t *testing.T) {
	e, plan, project, _ := acceptedParityDeliveryFixture(t)
	for n := 0; n < 2; n++ {
		requireWorkflowSuccess(t, e.Step(context.Background(), plan.ID))
	}
	var original provider.FinalizationResult
	loseCompletion := true
	adapter := func(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
		if err := runNativeFinalizerPeer(ctx, in); err != nil {
			return provider.Output{}, err
		}
		result, err := h.FinalizeDelivery(ctx)
		if err != nil {
			return provider.Output{}, err
		}
		if loseCompletion {
			original = result
			loseCompletion = false
			// Another committed update invalidates only the final plan write;
			// delivery reference and operation receipt have already committed.
			current, version := localStoredTask(t, e, plan.ID)
			if err := e.Store.Put(ctx, "execution_plans", plan.ID, current, version); err != nil {
				return provider.Output{}, err
			}
		} else if result != original {
			return provider.Output{}, errors.New("recovery created another delivery")
		}
		return provider.Output{Text: "Local accepted delivery."}, nil
	}
	e.Adapter = func(model.Provider) provider.Adapter { return connectionAdapterFunc(adapter) }
	if err := e.Step(context.Background(), plan.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("lost completion not reproduced: %v", err)
	}
	replacement := New(e.Store, e.GitHub, e.Root)
	replacement.PlansRoot = e.PlansRoot
	replacement.Adapter = func(model.Provider) provider.Adapter { return connectionAdapterFunc(adapter) }
	requireWorkflowSuccess(t, replacement.Step(context.Background(), plan.ID))
	saved, _ := localStoredTask(t, replacement, plan.ID)
	if saved.Stage != "complete" || saved.Delivery.DeliveryCommit != original.Commit || submissionGit(t, project.Path, finalizerFixtureRevParse, finalizerFixtureHeadsPrefix+original.Branch) != original.Commit {
		t.Fatal("restart failed to reconcile exact local delivery")
	}
}

func runFinalizerIdempotenceAcceptance(ctx context.Context, in provider.Input, h provider.Hooks, projectPath string, delivered *provider.FinalizationResult) (provider.Output, error) {
	if in.Purpose != "local_finalization" || !in.ReadOnly || in.CWD == projectPath {
		return provider.Output{}, errors.New("Finalizer isolation missing")
	}
	if err := runNativeFinalizerPeer(ctx, in); err != nil {
		return provider.Output{}, err
	}
	if h.FinalizeDelivery == nil {
		return provider.Output{}, errors.New("trusted finalization operation missing")
	}
	first, err := h.FinalizeDelivery(ctx)
	if err != nil {
		return provider.Output{}, err
	}
	second, err := h.FinalizeDelivery(ctx)
	if err != nil {
		return provider.Output{}, err
	}
	if first != second {
		return provider.Output{}, errors.New("repeated finalization produced different delivery")
	}
	*delivered = first
	return provider.Output{Text: "Local delivery ready. All required checks passed."}, nil
}

func assertFinalizerRejectsMutation(t *testing.T, mutation string) {
	t.Helper()
	e, plan, project, _ := acceptedParityDeliveryFixture(t)
	for n := 0; n < 2; n++ {
		requireWorkflowSuccess(t, e.Step(context.Background(), plan.ID))
	}
	var operationErr error
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
			if err := runNativeFinalizerPeer(ctx, in); err != nil {
				return provider.Output{}, err
			}
			if err := mutateFinalizerAcceptanceCandidate(t, ctx, e, plan, project, mutation, in); err != nil {
				return provider.Output{}, err
			}
			_, operationErr = h.FinalizeDelivery(ctx)
			return provider.Output{Text: "Finalization refused."}, nil
		})
	}
	if err := e.Step(context.Background(), plan.ID); err == nil || operationErr == nil {
		t.Fatalf("unaccepted state finalized: operation=%v Step=%v", operationErr, err)
	}
	saved, _ := localStoredTask(t, e, plan.ID)
	if saved.Stage == "complete" {
		t.Fatal("rejected finalization completed")
	}
	if _, err := submissionGitRun(context.Background(), project.Path, "show-ref", "--verify", "--hash", finalizerFixtureHeadsPrefix+saved.Delivery.Branch); err == nil {
		t.Fatal("rejected finalization published a branch")
	}
}

func mutateFinalizerAcceptanceCandidate(t *testing.T, ctx context.Context, e *Engine, plan model.ExecutionPlan, project model.Project, mutation string, in provider.Input) error {
	t.Helper()
	if mutation == "changed source" {
		if err := os.WriteFile(filepath.Join(in.CWD, finalizerFixtureResultPath), []byte("unaccepted change"), 0600); err != nil {
			return err
		}
	} else {
		current, version := localStoredTask(t, e, plan.ID)
		if mutation == "saved delivery tree" {
			current.Delivery.Tree = submissionGit(t, project.Path, finalizerFixtureRevParse, plan.Base+finalizerFixtureTreeSuffix)
		} else {
			current.Stage = "paused"
		}
		if err := e.Store.Put(ctx, "execution_plans", plan.ID, current, version); err != nil {
			return err
		}
	}
	return nil
}
