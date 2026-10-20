package workflow

import (
	"context"
	"encoding/json"
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
	return provider.Output{Text: "final reply", SessionID: "native-s"}, nil
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
	if out.Text != "final reply" {
		t.Fatal(out)
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
	e, task, _ := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	_, _ = e.Store.DB.Exec("DELETE FROM issue_tasks WHERE task_id=?", task.ID)
	task.Issue.Number = 0
	task.Stage = "plan_ready"
	task.Plan = &model.Plan{Objective: "publish", AcceptanceCriteria: []string{"exists"}}
	r, _ := e.Store.Get(ctx, "tasks", task.ID)
	if err := e.Store.Put(ctx, "tasks", task.ID, task, r.Version); err != nil {
		t.Fatal(err)
	}
	e.GitHub = publicationProbe{fakeGitHub: fakeGitHub{}, store: e.Store, t: t}
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	records, _ := e.Store.List(ctx, "publications")
	if len(records) != 1 {
		t.Fatal("publication result missing")
	}
	_ = p
}

type publicationProbe struct {
	fakeGitHub
	store *store.Store
	t     *testing.T
}

func (g publicationProbe) CreateIssue(ctx context.Context, repo, title, body, marker string) (model.Issue, error) {
	records, _ := g.store.List(ctx, "publications")
	if len(records) != 1 {
		g.t.Error("remote write preceded durable intent")
	}
	return model.Issue{Number: 2, Title: title, Body: body + "\n\n" + marker, State: "OPEN", Labels: []string{"whirlwind:plan"}}, nil
}

func TestVerificationEvidenceFailureBlocksProgress(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	if err := e.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	_, err := e.Store.DB.Exec("CREATE TRIGGER fail_verification BEFORE INSERT ON resources WHEN NEW.kind='verification' BEGIN SELECT RAISE(ABORT,'verification unavailable'); END")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Step(ctx, task.ID); err == nil {
		t.Fatal("verification persistence failure accepted")
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
