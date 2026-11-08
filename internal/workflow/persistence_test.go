package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type evidenceAgent struct {
	store *store.Store
	t     *testing.T
}

func (a evidenceAgent) Run(ctx context.Context, in provider.Input, h provider.Hooks) (provider.Output, error) {
	records, err := a.store.List(ctx, "sessions")
	if err != nil || len(records) != 1 {
		a.t.Fatalf("attempt must exist before execution: %v", err)
	}
	var state map[string]any
	_ = json.Unmarshal(records[0].Data, &state)
	if state["prompt_artifact"] == nil || state["policy_hash"] == nil || state["attempt_id"] != records[0].ID {
		a.t.Errorf("run input/provenance missing: %s", records[0].Data)
	}
	h.Event("session/update", map[string]any{"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "partial reply"}}})
	h.Event("item/agentMessage/delta", map[string]string{"delta": "visible reply"})
	out, err := (fakeAgent{9}).Run(ctx, in, h)
	out.SessionID = "native-s"
	return out, err
}

func TestAttemptPersistsInputStreamsAndMetrics(t *testing.T) {
	e, task, path := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	e.Adapter = func(model.Provider) provider.Adapter { return evidenceAgent{e.Store, t} }
	out, err := e.agent(ctx, p, task, provider.Input{Role: "plan", CWD: path, Prompt: "exact request", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	sessions, _ := e.Store.List(ctx, "sessions")
	metrics, _ := e.Store.List(ctx, "metrics")
	var metric map[string]any
	_ = json.Unmarshal(metrics[0].Data, &metric)
	if metric["attempt_id"] != sessions[0].ID {
		t.Fatalf("metrics not linked: %s", metrics[0].Data)
	}
	var plan model.Plan
	if err := json.Unmarshal([]byte(out.Text), &plan); err != nil || plan.Objective != "Add file" || out.SessionID != "native-s" {
		t.Fatalf("final planner output lost: %+v %v", out, err)
	}
	var n int
	if err = e.Store.DB.QueryRow("SELECT count(*) FROM attempt_events WHERE attempt_id=?", sessions[0].ID).Scan(&n); err != nil || n < 2 {
		t.Fatalf("stream evidence missing: %d %v", n, err)
	}
}

func TestAttemptCompletionRollsBackWhenMetricsFail(t *testing.T) {
	e, task, path := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	_, err := e.Store.DB.Exec("CREATE TRIGGER fail_metric BEFORE INSERT ON resources WHEN NEW.kind='metrics' BEGIN SELECT RAISE(ABORT,'injected metric failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.agent(ctx, p, task, provider.Input{Role: "plan", CWD: path, Prompt: "request", ReadOnly: true})
	if err == nil {
		t.Fatal("required evidence failure swallowed")
	}
	records, _ := e.Store.List(ctx, "sessions")
	var value struct{ Finished bool }
	_ = json.Unmarshal(records[0].Data, &value)
	if value.Finished {
		t.Fatal("completion committed without metrics")
	}
}

func TestAutomaticPermissionIsAudited(t *testing.T) {
	a := hookAttempt(t)
	a.project.Policy.Commands = [][]string{{"safe"}}
	allowed, err := a.permission(context.Background(), provider.Permission{Command: []string{"safe"}})
	if err != nil || !allowed {
		t.Fatal(allowed, err)
	}
	records, _ := a.Store.List(context.Background(), "audit")
	if len(records) == 0 {
		t.Fatal("automatic permission has no decision evidence")
	}
}

func TestPublicationIntentExistsBeforeRemoteWrite(t *testing.T) {
	e, task, _, _ := acceptedDeliveryFixture(t)
	ctx := context.Background()
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := store.Decode[model.Task](stored)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Delivery == nil || persisted.Delivery.Stage != "prepared" || persisted.Delivery.ReviewedCommit != task.ReviewedCommit {
		t.Fatalf("durable delivery intent does not bind accepted candidate: %+v", persisted.Delivery)
	}
	ref := "refs/heads/" + persisted.Delivery.Branch
	if _, err := submissionGitRun(ctx, task.Worktree, "show-ref", "--verify", ref); err == nil {
		t.Fatalf("delivery branch promoted before the intent checkpoint: %s", ref)
	}
	if _, err := os.Stat(persisted.Delivery.Worktree); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("delivery worktree exists before the intent checkpoint: err=%v", err)
	}
	publications, err := e.Store.List(ctx, "publications")
	if err != nil || len(publications) != 0 || persisted.PR != "" {
		t.Fatalf("local intent created remote publication state: PR=%q records=%d err=%v", persisted.PR, len(publications), err)
	}
}

func TestVerificationEvidenceFailureBlocksProgress(t *testing.T) {
	e, task, project, _ := acceptedDeliveryFixture(t)
	ctx := context.Background()
	_, err := e.Store.DB.Exec("CREATE TRIGGER fail_verification BEFORE INSERT ON resources WHEN NEW.kind='verification' BEGIN SELECT RAISE(ABORT,'verification unavailable'); END")
	if err != nil {
		t.Fatal(err)
	}
	previousVerification := task.VerificationID
	err = e.verify(ctx, project, &task)
	if !errors.Is(err, store.ErrPersistence) {
		t.Fatalf("verification persistence failure did not block progress: %v", err)
	}
	if task.Stage != "submitting" || task.VerificationID != previousVerification {
		t.Fatalf("failed verification advanced the accepted task: stage=%s verification=%s", task.Stage, task.VerificationID)
	}
	stored, err := e.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := store.Decode[model.Task](stored)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Stage != "submitting" || persisted.VerificationID != previousVerification {
		t.Fatalf("verification failure persisted workflow progress: stage=%s verification=%s", persisted.Stage, persisted.VerificationID)
	}
}

func TestCancelledAttemptRetainsTeardownEvidence(t *testing.T) {
	e, task, path := testEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, _ := e.Project(ctx, task.ProjectID)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			return provider.Output{}, nil
		})
	}
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, _ provider.Input, h provider.Hooks) (provider.Output, error) {
			cancel()
			h.Event("whirlwind/process_exit", map[string]any{"exit_code": -1})
			return provider.Output{}, context.Canceled
		})
	}
	out, _ := e.agent(ctx, p, task, provider.Input{Role: "plan", CWD: path, Prompt: "request", ReadOnly: true})
	events, err := e.Store.AttemptEvents(context.Background(), out.AttemptID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Kind == "whirlwind/process_exit" {
			found = true
		}
	}
	if !found {
		t.Fatal("teardown evidence lost after cancellation")
	}
}
