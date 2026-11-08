package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestAttemptLaunchPersistsSeparateRuntimeSignalsAndGeneration(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	first := newLeaseTestAttempt(e, task, project, cwd, "execution-one")
	first.input.Execution = &execution.Profile{Launcher: "/opt/whirlwind/bridges/sandbox/launch.mjs"}
	if err := first.launch(); err != nil {
		t.Fatal(err)
	}
	firstState := storedAttempt(t, e, first.id)
	if firstState.ExecutionID != first.id || firstState.ExecutionGeneration != 1 || firstState.WorkerHeartbeatAt == "" || firstState.LeaseExpiresAt == "" {
		t.Fatalf("first execution did not persist its owner lease: %+v", firstState)
	}
	if firstState.RuntimeKind != "local" || firstState.RuntimeLauncher != first.input.Execution.Launcher {
		t.Fatalf("attempt did not persist its effective runtime kind and launcher: kind=%q launcher=%q", firstState.RuntimeKind, firstState.RuntimeLauncher)
	}
	if firstState.ProviderProgressAt != firstState.StartedAt || firstState.ProcessState != "starting" || firstState.SandboxHealth != "unknown" {
		t.Fatalf("runtime signals were collapsed or omitted at launch: %+v", firstState)
	}
	if firstState.ConfigSnapshotVersion != 1 || firstState.ConfigSnapshotHash == "" {
		t.Fatalf("execution configuration snapshot was not pinned: %+v", firstState)
	}
	firstBeat, _ := time.Parse(time.RFC3339Nano, firstState.WorkerHeartbeatAt)
	leaseExpiry, _ := time.Parse(time.RFC3339Nano, firstState.LeaseExpiresAt)
	if !leaseExpiry.Equal(firstBeat.Add(runtimeLeaseLifetime)) {
		t.Fatalf("worker lease expires at %s, want heartbeat + %s", leaseExpiry, runtimeLeaseLifetime)
	}
	firstDeadline, err := attemptDeadline(firstState)
	if err != nil || !firstDeadline.Equal(firstBeat.Add(attemptWallClockLimit)) {
		t.Fatalf("runtime lease changed the attempt deadline: %s %v", firstDeadline, err)
	}

	second := newLeaseTestAttempt(e, task, project, cwd, "execution-two")
	if err := second.launch(); err != nil {
		t.Fatal(err)
	}
	if state := storedAttempt(t, e, second.id); state.ExecutionGeneration != 2 {
		t.Fatalf("new execution generation = %d, want 2", state.ExecutionGeneration)
	}
}

func TestWorkerHeartbeatPersistsObservedOpenShellHealth(t *testing.T) {
	e, task, cwd := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	attempt := newLeaseTestAttempt(e, task, project, cwd, "observed-sandbox-health")
	if err := attempt.launch(); err != nil {
		t.Fatal(err)
	}
	state := storedAttempt(t, e, attempt.id)
	sidecar := filepath.Join(t.TempDir(), "openshell.json")
	state.SandboxName = "whirlwind-observed"
	state.LifecycleFile = sidecar
	state.GatewayName = "gateway-one"
	state.GatewayEndpoint = "http://127.0.0.1:8080"
	state.Workspace = "workspace-one"
	proof := execution.SandboxLifecycle{UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano), ExecutionID: state.ExecutionID, DeadlineAt: state.DeadlineAt, ExecutionLabelValue: state.ExecutionLabelValue, ExecutionGeneration: state.ExecutionGeneration, SandboxName: state.SandboxName, SandboxID: "sandbox-123", GatewayName: state.GatewayName, GatewayEndpoint: state.GatewayEndpoint, Workspace: state.Workspace, SandboxPhase: "Ready", SandboxHealth: "healthy"}
	data, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecar, data, 0600); err != nil {
		t.Fatal(err)
	}
	record, err := e.Store.Get(context.Background(), "sessions", attempt.id)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(context.Background(), "sessions", attempt.id, state, record.Version); err != nil {
		t.Fatal(err)
	}
	if err := attempt.recordWorkerHeartbeat(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	observed := storedAttempt(t, e, attempt.id)
	if observed.SandboxID != proof.SandboxID || observed.SandboxPhase != proof.SandboxPhase || observed.SandboxHealth != proof.SandboxHealth {
		t.Fatalf("heartbeat did not persist independently observed sandbox state: %+v", observed)
	}
	if observed.DeadlineAt != state.DeadlineAt {
		t.Fatalf("observed lifecycle changed hard attempt deadline: %s != %s", observed.DeadlineAt, state.DeadlineAt)
	}
}

func TestWorkerHeartbeatMarksOldOpenShellProofStale(t *testing.T) {
	attempt, state, e, proof := openShellHealthFixture(t)
	proof.UpdatedAt = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	writeOpenShellProof(t, state.LifecycleFile, proof)
	if err := attempt.recordWorkerHeartbeat(context.Background(), time.Now().UTC()); !errors.Is(err, ErrRuntimeHealthLost) {
		t.Fatalf("stale sandbox health did not signal runtime failure: %v", err)
	}
	observed := storedAttempt(t, e, attempt.id)
	if observed.SandboxHealth != "stale" || observed.SandboxPhase != "Ready" {
		t.Fatalf("expired health proof was treated as a fresh healthy sandbox: phase=%q health=%q", observed.SandboxPhase, observed.SandboxHealth)
	}
}

func TestWorkerHeartbeatCancelsAdapterWhenSandboxHealthIsLost(t *testing.T) {
	attempt, state, e, proof := openShellHealthFixture(t)
	proof.UpdatedAt = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	writeOpenShellProof(t, state.LifecycleFile, proof)
	ctx, cancel := context.WithCancel(context.Background())
	attempt.mu.Lock()
	attempt.cancel = cancel
	attempt.mu.Unlock()
	stop := attempt.startWorkerHeartbeatAtInterval(ctx, 10*time.Millisecond)
	defer stop()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("independent heartbeat did not cancel the adapter after sandbox health loss")
	}
	attempt.mu.Lock()
	err := attempt.eventErr
	attempt.mu.Unlock()
	if !errors.Is(err, ErrRuntimeHealthLost) {
		t.Fatalf("sandbox health cancellation lost its typed failure: %v", err)
	}
	if observed := storedAttempt(t, e, attempt.id); observed.SandboxHealth != "stale" {
		t.Fatalf("health loss was not durably recorded: %+v", observed)
	}
}

func TestExpiredWorkerLeaseCannotBeRenewed(t *testing.T) {
	e, task, cwd := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	attempt := newLeaseTestAttempt(e, task, project, cwd, "expired-worker-lease")
	if err := attempt.launch(); err != nil {
		t.Fatal(err)
	}
	state := storedAttempt(t, e, attempt.id)
	expired := time.Now().UTC().Add(-time.Second)
	state.LeaseExpiresAt = expired.Format(time.RFC3339Nano)
	record, err := e.Store.Get(context.Background(), "sessions", attempt.id)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(context.Background(), "sessions", attempt.id, state, record.Version); err != nil {
		t.Fatal(err)
	}
	if err := attempt.recordWorkerHeartbeat(context.Background(), time.Now().UTC()); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("expired execution lease was revived by a late heartbeat: %v", err)
	}
}

func TestExpiredWorkerLeaseCannotCommitLateResult(t *testing.T) {
	e, task, cwd := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	attempt := newLeaseTestAttempt(e, task, project, cwd, "expired-result-owner")
	if err := attempt.launch(); err != nil {
		t.Fatal(err)
	}
	state := storedAttempt(t, e, attempt.id)
	state.LeaseExpiresAt = time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
	record, err := e.Store.Get(context.Background(), "sessions", attempt.id)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(context.Background(), "sessions", attempt.id, state, record.Version); err != nil {
		t.Fatal(err)
	}
	if err := attempt.finish(provider.Output{Text: "late response"}, nil, time.Now().Add(-time.Second)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("expired owner committed a late result: %v", err)
	}
}

func TestExpiredWorkerLeaseFencesLateResultsForEveryRole(t *testing.T) {
	for _, role := range model.AgentRoles() {
		t.Run(role, func(t *testing.T) {
			e, task, cwd := testEngine(t)
			project, err := e.Project(context.Background(), task.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			attempt := newLeaseTestAttempt(e, task, project, cwd, "expired-"+role)
			attempt.input.Role = role
			if err := attempt.launch(); err != nil {
				t.Fatal(err)
			}
			state := storedAttempt(t, e, attempt.id)
			state.LeaseExpiresAt = time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
			record, err := e.Store.Get(context.Background(), "sessions", attempt.id)
			if err != nil {
				t.Fatal(err)
			}
			if err := e.Store.Put(context.Background(), "sessions", attempt.id, state, record.Version); err != nil {
				t.Fatal(err)
			}
			if err := attempt.finish(provider.Output{Text: "late response"}, nil, time.Now().Add(-time.Second)); !errors.Is(err, store.ErrConflict) {
				t.Fatalf("expired %s owner committed a late result: %v", role, err)
			}
		})
	}
}

func TestSupersededProviderCannotCommitAfterFallbackTakesOwnership(t *testing.T) {
	e, task, cwd := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	primary := newLeaseTestAttempt(e, task, project, cwd, "primary-runtime-owner")
	if err := primary.launch(); err != nil {
		t.Fatal(err)
	}
	state := storedAttempt(t, e, primary.id)
	state.Provider = "fallback-provider"
	state.ProviderName = "Fallback"
	state.ExecutionID = "fallback-execution"
	state.ExecutionGeneration++
	state.LeaseExpiresAt = time.Now().UTC().Add(runtimeLeaseLifetime).Format(time.RFC3339Nano)
	record, err := e.Store.Get(context.Background(), "sessions", primary.id)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(context.Background(), "sessions", primary.id, state, record.Version); err != nil {
		t.Fatal(err)
	}
	if err := primary.finish(provider.Output{Text: "stale primary response"}, nil, time.Now().Add(-time.Second)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("superseded provider committed after fallback ownership changed: %v", err)
	}
}

func TestMalformedPlannerOutputIsRejectedBeforeAttemptSuccess(t *testing.T) {
	attempt := &agentAttempt{agentRun: &agentRun{input: provider.Input{Role: "plan"}}}
	err := attemptResultError(attempt, provider.Output{Text: "{"}, nil)
	if !errors.Is(err, ErrMalformedRoleOutput) {
		t.Fatalf("malformed planner output was allowed to reach success persistence: %v", err)
	}
	kind, failure := attempt.failureKind(err)
	if failure != nil || kind != provider.FailureTransient || !providerRetryableError(err.Error()) {
		t.Fatalf("malformed role output did not consume bounded transient recovery budget: kind=%s err=%v", kind, failure)
	}
	if count := nextRestartCount(model.RecoveryEpisode{Status: "failed", FailureKind: string(provider.FailureTransient), Error: err.Error(), RestartCount: 1}); count != 2 {
		t.Fatalf("malformed role output reset recovery episode: restart count=%d", count)
	}
	if count := nextRestartCount(model.RecoveryEpisode{Status: "failed", FailureKind: string(provider.FailureUnknown), Error: err.Error(), RestartCount: 1}); count != 1 {
		t.Fatalf("explicit unknown classification was overridden by error text: restart count=%d", count)
	}
}

func TestValidLowAdherenceReviewAndHumanPauseAreNotMalformedOutput(t *testing.T) {
	review := &agentAttempt{agentRun: &agentRun{input: provider.Input{Role: "review"}}}
	validFailingReview := `{"summary":"One requirement is not met yet.","findings":[],"requirements":[{"id":"R1","status":"fail","explanation":"Evidence is missing."}]}`
	if err := attemptResultError(review, provider.Output{Text: validFailingReview}, nil); err != nil {
		t.Fatalf("valid low-adherence review was treated as malformed: %v", err)
	}
	plan := &agentAttempt{agentRun: &agentRun{input: provider.Input{Role: "plan"}}}
	if err := attemptResultError(plan, provider.Output{Text: "{"}, ErrHumanDecisionPending); !errors.Is(err, ErrHumanDecisionPending) || errors.Is(err, ErrMalformedRoleOutput) {
		t.Fatalf("intentional human pause was converted into malformed-output retry: %v", err)
	}
}

func openShellHealthFixture(t *testing.T) (*agentAttempt, model.Attempt, *Engine, execution.SandboxLifecycle) {
	return openShellHealthFixtureForRole(t, "code")
}

func openShellHealthFixtureForRole(t *testing.T, role string) (*agentAttempt, model.Attempt, *Engine, execution.SandboxLifecycle) {
	t.Helper()
	e, task, cwd := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	attempt := newLeaseTestAttempt(e, task, project, cwd, "observed-sandbox-health")
	attempt.input.Role = role
	if err := attempt.launch(); err != nil {
		t.Fatal(err)
	}
	state := storedAttempt(t, e, attempt.id)
	state.SandboxName = "whirlwind-observed"
	state.LifecycleFile = filepath.Join(t.TempDir(), "openshell.json")
	state.GatewayName = "gateway-one"
	state.GatewayEndpoint = "http://127.0.0.1:8080"
	state.Workspace = "workspace-one"
	proof := execution.SandboxLifecycle{UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano), ExecutionID: state.ExecutionID, DeadlineAt: state.DeadlineAt, ExecutionLabelValue: state.ExecutionLabelValue, ExecutionGeneration: state.ExecutionGeneration, SandboxName: state.SandboxName, SandboxID: "sandbox-123", GatewayName: state.GatewayName, GatewayEndpoint: state.GatewayEndpoint, Workspace: state.Workspace, SandboxPhase: "Ready", SandboxHealth: "healthy"}
	writeOpenShellProof(t, state.LifecycleFile, proof)
	record, err := e.Store.Get(context.Background(), "sessions", attempt.id)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(context.Background(), "sessions", attempt.id, state, record.Version); err != nil {
		t.Fatal(err)
	}
	return attempt, state, e, proof
}

func TestLostSandboxHealthFencesAdaptersForEveryRole(t *testing.T) {
	for _, role := range model.AgentRoles() {
		t.Run(role, func(t *testing.T) {
			attempt, state, _, proof := openShellHealthFixtureForRole(t, role)
			proof.UpdatedAt = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
			writeOpenShellProof(t, state.LifecycleFile, proof)
			ctx, cancel := context.WithCancel(context.Background())
			attempt.mu.Lock()
			attempt.cancel = cancel
			attempt.mu.Unlock()
			stop := attempt.startWorkerHeartbeatAtInterval(ctx, 10*time.Millisecond)
			defer stop()
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatalf("%s adapter continued after sandbox health loss", role)
			}
			attempt.mu.Lock()
			err := attempt.eventErr
			attempt.mu.Unlock()
			if !errors.Is(err, ErrRuntimeHealthLost) {
				t.Fatalf("%s lost sandbox health without typed runtime failure: %v", role, err)
			}
		})
	}
}

func writeOpenShellProof(t *testing.T, path string, proof execution.SandboxLifecycle) {
	t.Helper()
	data, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerHeartbeatRenewsLeaseWithoutExtendingAttemptAndFencesStaleGeneration(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	attempt := newLeaseTestAttempt(e, task, project, cwd, "heartbeat-attempt")
	if err := attempt.launch(); err != nil {
		t.Fatal(err)
	}
	state := storedAttempt(t, e, attempt.id)
	originalDeadline := state.DeadlineAt
	oldLease, _ := time.Parse(time.RFC3339Nano, state.LeaseExpiresAt)
	beatAt := time.Now().UTC()
	if err := attempt.recordWorkerHeartbeat(ctx, beatAt); err != nil {
		t.Fatal(err)
	}
	state = storedAttempt(t, e, attempt.id)
	newLease, _ := time.Parse(time.RFC3339Nano, state.LeaseExpiresAt)
	workerBeat, _ := time.Parse(time.RFC3339Nano, state.WorkerHeartbeatAt)
	if !newLease.After(oldLease) || state.DeadlineAt != originalDeadline || workerBeat.Before(beatAt) || workerBeat.After(beatAt.Add(time.Second)) {
		t.Fatalf("heartbeat extended attempt deadline or failed to renew lease: %+v", state)
	}
	state.ExecutionGeneration++
	record, err := e.Store.Get(ctx, "sessions", attempt.id)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(ctx, "sessions", attempt.id, state, record.Version); err != nil {
		t.Fatal(err)
	}
	if err := attempt.recordWorkerHeartbeat(ctx, beatAt.Add(time.Second)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale owner heartbeat was accepted: %v", err)
	}
}

func TestDelayedWorkerHeartbeatCannotRenewFromOldTickerSample(t *testing.T) {
	e, task, cwd := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	attempt := newLeaseTestAttempt(e, task, project, cwd, "delayed-heartbeat-sample")
	if err := attempt.launch(); err != nil {
		t.Fatal(err)
	}
	state := storedAttempt(t, e, attempt.id)
	state.LeaseExpiresAt = time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)
	record, err := e.Store.Get(context.Background(), "sessions", attempt.id)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.Put(context.Background(), "sessions", attempt.id, state, record.Version); err != nil {
		t.Fatal(err)
	}
	oldSample := time.Now().UTC().Add(-10 * time.Second)
	if err := attempt.recordWorkerHeartbeat(context.Background(), oldSample); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("late heartbeat sample renewed an owner lease: %v", err)
	}
}

func TestWorkerHeartbeatRunsIndependentlyOfProviderOutput(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	attempt := newLeaseTestAttempt(e, task, project, cwd, "independent-heartbeat")
	if err := attempt.launch(); err != nil {
		t.Fatal(err)
	}
	initial := storedAttempt(t, e, attempt.id)
	stop := attempt.startWorkerHeartbeatAtInterval(ctx, 10*time.Millisecond)
	time.Sleep(45 * time.Millisecond)
	stop()
	updated := storedAttempt(t, e, attempt.id)
	if updated.WorkerHeartbeatAt == initial.WorkerHeartbeatAt {
		t.Fatal("worker heartbeat depended on provider output or ACP events")
	}
	if updated.ProviderProgressAt != initial.ProviderProgressAt {
		t.Fatalf("worker heartbeat was reported as provider progress: initial=%s updated=%s", initial.ProviderProgressAt, updated.ProviderProgressAt)
	}
	if updated.DeadlineAt != initial.DeadlineAt {
		t.Fatal("independent worker heartbeat extended the hard attempt deadline")
	}
}

func newLeaseTestAttempt(e *Engine, task model.Task, project model.Project, cwd, id string) *agentAttempt {
	run := &agentRun{Engine: e, ctx: context.Background(), project: project, task: task, input: provider.Input{Role: "code", Purpose: "primary", Model: "fixture-model", CWD: cwd, ExecutionProfile: "fixture-profile"}}
	return &agentAttempt{agentRun: run, id: id, chosen: model.Provider{ID: "fixture", Name: "Fixture", Kind: "acp"}}
}

func storedAttempt(t *testing.T, e *Engine, id string) model.Attempt {
	t.Helper()
	record, err := e.Store.Get(context.Background(), "sessions", id)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Decode[model.Attempt](record)
	if err != nil {
		t.Fatal(err)
	}
	return state
}
